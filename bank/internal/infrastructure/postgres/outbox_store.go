package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

const enqueueOutboxSQL = `
INSERT INTO outbox_messages (topic, aggregate_id, payload, correlation_id)
VALUES ($1, $2, $3, $4)`

// claimOutboxSQL leases ready messages: pending, due and with no live lease.
// SKIP LOCKED lets relays claim side by side; the token comes from a global
// sequence, so a later lease always has a larger one.
const claimOutboxSQL = `
UPDATE outbox_messages
SET lease_owner = $1,
    lease_token = nextval('outbox_lease_token_seq'),
    lease_expires_at = now() + $2::bigint * interval '1 millisecond',
    attempts = attempts + 1
WHERE id IN (
    SELECT id FROM outbox_messages
    WHERE status = 'pending'
      AND next_attempt_at <= now()
      AND (lease_expires_at IS NULL OR lease_expires_at <= now())
    ORDER BY next_attempt_at, id
    LIMIT $3
    FOR UPDATE SKIP LOCKED)
RETURNING id, aggregate_id, lease_token, attempts, correlation_id`

// finishOutboxSQL is the fencing check: only the holder of the current
// token closes the message, even after its lease expired, as long as no one
// took a newer one.
const finishOutboxSQL = `
UPDATE outbox_messages
SET status = 'delivered', delivered_at = now(), lease_owner = NULL, lease_expires_at = NULL
WHERE id = $1 AND lease_token = $2 AND status = 'pending'`

// advanceStatusSQL is the compare-and-set.
const advanceStatusSQL = `
UPDATE external_transfers
SET status = $2::external_transfer_status,
    status_seq = status_seq + 1,
    failure_code = CASE WHEN $2::external_transfer_status = 'failed' THEN $3 ELSE NULL END,
    updated_at = now()
WHERE id = $1 AND status_seq = $4`

// errStaleStatus is a compare-and-set that lost: the caller read the status
// without the lock, which the guarded machine never does.
var errStaleStatus = errors.New("transfer status changed since it was read")

// deferOutboxSQL hands the message back for later, under the same fencing
// as finishOutboxSQL.
const deferOutboxSQL = `
UPDATE outbox_messages
SET next_attempt_at = now() + $3::bigint * interval '1 millisecond', last_error = $4,
    lease_owner = NULL, lease_expires_at = NULL
WHERE id = $1 AND lease_token = $2 AND status = 'pending'`

// insertInboxSQL is the inbox defense's intake: the unique (source, event_id)
// of the inbox migration turns a redelivery into nothing.
const insertInboxSQL = `
INSERT INTO inbox_events (event_id, event_type, end_to_end_id, payload, correlation_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (source, event_id) DO NOTHING`

// claimInboxSQL takes the oldest ready event and keeps it locked for the
// rest of the transaction, which also applies it and marks it.
const claimInboxSQL = `
UPDATE inbox_events
SET attempts = attempts + 1
WHERE id = (
    SELECT id FROM inbox_events
    WHERE status = 'pending' AND next_attempt_at <= now()
    ORDER BY id
    LIMIT 1
    FOR UPDATE SKIP LOCKED)
RETURNING id, event_id, event_type, end_to_end_id, payload, correlation_id`

// reclaimInboxSQL skips an event another processor holds: it is that
// processor's to apply now.
const reclaimInboxSQL = `SELECT id FROM inbox_events WHERE id = $1 AND status = 'pending' FOR UPDATE SKIP LOCKED`

const finishInboxSQL = `
UPDATE inbox_events
SET status = $2::inbox_status, outcome = $3, processed_at = now()
WHERE id = $1 AND status = 'pending'`

const deferInboxSQL = `
UPDATE inbox_events
SET next_attempt_at = now() + $2::bigint * interval '1 millisecond', outcome = $3
WHERE id = $1 AND status = 'pending'`

const lockExternalTransferSQL = `SELECT ` + externalTransferColumns + ` FROM external_transfers WHERE id = $1 FOR UPDATE`

// outboxStore keeps the outbox and the locks of the transfers it relays.
type outboxStore struct {
	db querier
}

// inboxStore keeps the partner events waiting to be applied.
type inboxStore struct {
	db querier
}

type inboxPayload struct {
	AmountCents int64  `json:"amount_cents"`
	Sequence    int    `json:"sequence"`
	Reason      string `json:"reason"`
	AccountID   string `json:"account_id"`
}

// EnqueueOutbox writes the message in the caller's transaction.
func (s outboxStore) EnqueueOutbox(ctx context.Context, message application.OutboxMessage) error {
	enqueue := newStatement("enqueueing outbox message", enqueueOutboxSQL, message.Topic, message.AggregateID, message.Payload, message.CorrelationID)
	_, err := execute(ctx, s.db, enqueue)

	return err
}

// ClaimOutbox leases up to limit messages.
func (s outboxStore) ClaimOutbox(ctx context.Context, owner string, ttl time.Duration, limit int) ([]application.OutboxClaim, error) {
	claim := newStatement("claiming outbox messages", claimOutboxSQL, owner, ttl.Milliseconds(), limit)

	return queryRows(ctx, s.db, claim, scanOutboxClaim)
}

// FinishOutbox marks the message delivered if the token still owns it.
func (s outboxStore) FinishOutbox(ctx context.Context, id, token int64) (bool, error) {
	finished, err := execute(ctx, s.db, newStatement("finishing outbox message", finishOutboxSQL, id, token))

	return finished == 1, err
}

// LockExternalTransfer locks the transfer until the transaction ends and
// returns it.
func (s outboxStore) LockExternalTransfer(ctx context.Context, id transfer.ID) (application.ExternalTransfer, error) {
	return readExternalTransfer(ctx, s.db, newStatement("locking external transfer", lockExternalTransferSQL, uuid.UUID(id)))
}

// AdvanceStatus changes the status if the sequence is still the one seen.
func (s outboxStore) AdvanceStatus(ctx context.Context, change application.StatusChange, seenSeq int) error {
	advance := newStatement("advancing status", advanceStatusSQL, uuid.UUID(change.TransferID), string(change.To), change.FailureCode, seenSeq)

	return executeOne(ctx, s.db, advance, errStaleStatus)
}

// DeferOutbox reschedules the message if the token still owns it.
func (s outboxStore) DeferOutbox(ctx context.Context, claim application.OutboxClaim, delay time.Duration, reason string) (bool, error) {
	deferral := newStatement("deferring outbox message", deferOutboxSQL, claim.ID, claim.Token, delay.Milliseconds(), reason)
	deferred, err := execute(ctx, s.db, deferral)

	return deferred == 1, err
}

// InsertInbox records the event unless it is already there.
func (s inboxStore) InsertInbox(ctx context.Context, event application.PartnerEvent, correlationID string) (bool, error) {
	fields := inboxPayload{AmountCents: int64(event.AmountCents), Sequence: event.Sequence, Reason: event.Reason, AccountID: event.AccountID}

	payload, err := json.Marshal(fields)
	if err != nil {
		return false, err
	}

	insert := newStatement("inserting inbox event", insertInboxSQL, event.ID, event.Type, event.EndToEndID, payload, correlationID)
	inserted, err := execute(ctx, s.db, insert)

	return inserted == 1, err
}

// ReclaimInbox locks a pending event no other transaction holds.
func (s inboxStore) ReclaimInbox(ctx context.Context, id int64) (bool, error) {
	var locked int64

	return scanOptionalRow(ctx, s.db, newStatement("reclaiming inbox event", reclaimInboxSQL, id), &locked)
}

// ClaimInbox returns the next event to apply, or false when none is ready.
func (s inboxStore) ClaimInbox(ctx context.Context) (application.InboxEvent, bool, error) {
	var (
		event   application.InboxEvent
		payload []byte
	)

	claim := newStatement("claiming inbox event", claimInboxSQL)

	isClaimed, err := scanOptionalRow(ctx, s.db, claim, &event.ID, &event.Event.ID, &event.Event.Type, &event.Event.EndToEndID, &payload, &event.CorrelationID)
	if err != nil {
		return application.InboxEvent{}, false, err
	}

	if !isClaimed {
		return application.InboxEvent{}, false, nil
	}

	var fields inboxPayload

	err = json.Unmarshal(payload, &fields)
	if err != nil {
		return application.InboxEvent{}, false, fmt.Errorf("decoding inbox event %d: %w", event.ID, err)
	}

	event.Event.AmountCents = money.Cents(fields.AmountCents)
	event.Event.Sequence = fields.Sequence
	event.Event.Reason = fields.Reason
	event.Event.AccountID = fields.AccountID

	return event, true, nil
}

// FinishInbox closes the event with its outcome.
func (s inboxStore) FinishInbox(ctx context.Context, id int64, status application.InboxStatus, outcome string) error {
	_, err := execute(ctx, s.db, newStatement("finishing inbox event", finishInboxSQL, id, string(status), outcome))

	return err
}

// DeferInbox puts the event back for a later attempt.
func (s inboxStore) DeferInbox(ctx context.Context, id int64, delay time.Duration, reason string) error {
	_, err := execute(ctx, s.db, newStatement("deferring inbox event", deferInboxSQL, id, delay.Milliseconds(), reason))

	return err
}

func scanOutboxClaim(row pgx.CollectableRow) (application.OutboxClaim, error) {
	var claim application.OutboxClaim
	err := row.Scan(&claim.ID, &claim.AggregateID, &claim.Token, &claim.Attempts, &claim.CorrelationID)

	return claim, err
}
