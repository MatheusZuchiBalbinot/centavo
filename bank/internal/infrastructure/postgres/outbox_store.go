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

// EnqueueOutbox writes the message in the caller's transaction.
func (s LedgerStore) EnqueueOutbox(ctx context.Context, message application.OutboxMessage) error {
	_, err := s.db.Exec(ctx, enqueueOutboxSQL, message.Topic, message.AggregateID, message.Payload, message.CorrelationID)
	if err != nil {
		return fmt.Errorf("enqueueing outbox message: %w", err)
	}

	return nil
}

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
RETURNING id, aggregate_id, lease_token, correlation_id`

// ClaimOutbox leases up to limit messages.
func (s LedgerStore) ClaimOutbox(ctx context.Context, owner string, ttl time.Duration, limit int) ([]application.OutboxClaim, error) {
	rows, err := s.db.Query(ctx, claimOutboxSQL, owner, ttl.Milliseconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("claiming outbox messages: %w", err)
	}

	return pgx.CollectRows(rows, scanOutboxClaim)
}

func scanOutboxClaim(row pgx.CollectableRow) (application.OutboxClaim, error) {
	var claim application.OutboxClaim
	err := row.Scan(&claim.ID, &claim.AggregateID, &claim.Token, &claim.CorrelationID)

	return claim, err
}

// finishOutboxSQL is the fencing check: only the holder of the current
// token closes the message, even after its lease expired, as long as no one
// took a newer one.
const finishOutboxSQL = `
UPDATE outbox_messages
SET status = 'delivered', delivered_at = now(), lease_owner = NULL, lease_expires_at = NULL
WHERE id = $1 AND lease_token = $2 AND status = 'pending'`

// FinishOutbox marks the message delivered if the token still owns it.
func (s LedgerStore) FinishOutbox(ctx context.Context, id, token int64) (bool, error) {
	tag, err := s.db.Exec(ctx, finishOutboxSQL, id, token)
	if err != nil {
		return false, fmt.Errorf("finishing outbox message: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

const lockExternalTransferSQL = `SELECT status::text FROM external_transfers WHERE id = $1 FOR UPDATE`

// LockExternalTransfer locks the transfer until the transaction ends and
// returns its status.
func (s LedgerStore) LockExternalTransfer(ctx context.Context, id transfer.ID) (transfer.Status, error) {
	var status string

	err := s.db.QueryRow(ctx, lockExternalTransferSQL, uuid.UUID(id)).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", transfer.ErrTransferNotFound
	}

	if err != nil {
		return "", fmt.Errorf("locking external transfer: %w", err)
	}

	return transfer.Status(status), nil
}

// insertInboxSQL is defense D5's intake: the unique (source, event_id) of the
// inbox migration turns a redelivery into nothing.
const insertInboxSQL = `
INSERT INTO inbox_events (event_id, event_type, end_to_end_id, payload, correlation_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (source, event_id) DO NOTHING`

type inboxPayload struct {
	AmountCents int64  `json:"amount_cents"`
	Sequence    int    `json:"sequence"`
	Reason      string `json:"reason"`
	AccountID   string `json:"account_id"`
}

// InsertInbox records the event unless it is already there.
func (s LedgerStore) InsertInbox(ctx context.Context, event application.PartnerEvent, correlationID string) (bool, error) {
	payload, err := json.Marshal(inboxPayload{AmountCents: int64(event.AmountCents), Sequence: event.Sequence, Reason: event.Reason, AccountID: event.AccountID})
	if err != nil {
		return false, err
	}

	tag, err := s.db.Exec(ctx, insertInboxSQL, event.ID, event.Type, event.EndToEndID, payload, correlationID)
	if err != nil {
		return false, fmt.Errorf("inserting inbox event: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

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

// ClaimInbox returns the next event to apply, or false when none is ready.
func (s LedgerStore) ClaimInbox(ctx context.Context) (application.InboxEvent, bool, error) {
	var (
		event   application.InboxEvent
		payload []byte
	)

	err := s.db.QueryRow(ctx, claimInboxSQL).Scan(&event.ID, &event.Event.ID, &event.Event.Type, &event.Event.EndToEndID, &payload, &event.CorrelationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.InboxEvent{}, false, nil
	}

	if err != nil {
		return application.InboxEvent{}, false, fmt.Errorf("claiming inbox event: %w", err)
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

const finishInboxSQL = `
UPDATE inbox_events
SET status = $2::inbox_status, outcome = $3, processed_at = now()
WHERE id = $1 AND status = 'pending'`

// FinishInbox closes the event with its outcome.
func (s LedgerStore) FinishInbox(ctx context.Context, id int64, status application.InboxStatus, outcome string) error {
	_, err := s.db.Exec(ctx, finishInboxSQL, id, string(status), outcome)
	if err != nil {
		return fmt.Errorf("finishing inbox event: %w", err)
	}

	return nil
}

const deferInboxSQL = `
UPDATE inbox_events
SET next_attempt_at = now() + $2::bigint * interval '1 millisecond', outcome = $3
WHERE id = $1 AND status = 'pending'`

// DeferInbox puts the event back for a later attempt.
func (s LedgerStore) DeferInbox(ctx context.Context, id int64, delay time.Duration, reason string) error {
	_, err := s.db.Exec(ctx, deferInboxSQL, id, delay.Milliseconds(), reason)
	if err != nil {
		return fmt.Errorf("deferring inbox event: %w", err)
	}

	return nil
}
