package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
)

// claimOutboxSQL leases ready messages: pending, due and with no live lease.
// SKIP LOCKED lets relays claim side by side; the token comes from a global
// sequence, so a later lease always has a larger one.
const claimOutboxSQL = `
UPDATE outbox_messages
SET lease_owner = @owner,
    lease_token = nextval('outbox_lease_token_seq'),
    lease_expires_at = now() + @ttl_ms::bigint * interval '1 millisecond',
    attempts = attempts + 1
WHERE id IN (
    SELECT id FROM outbox_messages
    WHERE status = 'pending'
      AND next_attempt_at <= now()
      AND (lease_expires_at IS NULL OR lease_expires_at <= now())
    ORDER BY next_attempt_at, id
    LIMIT @limit
    FOR UPDATE SKIP LOCKED)
RETURNING id, aggregate_id, lease_token, attempts, correlation_id`

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
const reclaimInboxSQL = `SELECT id FROM inbox_events WHERE id = @id AND status = 'pending' FOR UPDATE SKIP LOCKED`

// Fencing: only the holder of the current token closes or defers a message,
// even after its lease expired, as long as no one took a newer one.
const ownedByLeaseSQL = "id = @id AND lease_token = @token AND status = 'pending'"

// afterDelaySQL is now plus a delay given in milliseconds.
const afterDelaySQL = "now() + @delay_ms::bigint * interval '1 millisecond'"

// failureCodeSQL keeps the failure code only on a transfer that failed.
const failureCodeSQL = "failure_code = CASE WHEN @status::external_transfer_status = 'failed' THEN @failure_code ELSE NULL END"

// errStaleStatus is a compare-and-set that lost: the caller read the status
// without the lock, which the guarded machine never does.
var errStaleStatus = errors.New("transfer status changed since it was read")

// outboxStore keeps the outbox and the locks of the transfers it relays.
type outboxStore struct {
	db querier
}

// inboxStore keeps the partner events waiting to be applied.
type inboxStore struct {
	db querier
}

// EnqueueOutbox writes the message in the caller's transaction.
func (s outboxStore) EnqueueOutbox(ctx context.Context, message application.OutboxMessage) error {
	_, err := insertRow("enqueueing outbox message", outboxMessagesTable, outboxMessageRowOf(message)).Exec(ctx, s.db)

	return err
}

// ClaimOutbox leases up to limit messages.
func (s outboxStore) ClaimOutbox(ctx context.Context, owner string, ttl time.Duration, limit int) ([]application.OutboxClaim, error) {
	values := args{"owner": owner, "ttl_ms": ttl.Milliseconds(), "limit": limit}

	rows, err := queryRows(ctx, s.db, newStatement("claiming outbox messages", claimOutboxSQL, values), pgx.RowToStructByName[outboxClaimRow])
	if err != nil {
		return nil, err
	}

	claims := make([]application.OutboxClaim, len(rows))

	for k, row := range rows {
		claims[k] = row.toDomain()
	}

	return claims, nil
}

// FinishOutbox marks the message delivered if the token still owns it.
func (s outboxStore) FinishOutbox(ctx context.Context, id, token int64) (bool, error) {
	finish := update("finishing outbox message", outboxMessagesTable).
		SetSQL("status = 'delivered', delivered_at = now(), lease_owner = NULL, lease_expires_at = NULL", nil).
		Where(ownedByLeaseSQL, args{"id": id, "token": token})

	finished, err := finish.Exec(ctx, s.db)

	return finished == 1, err
}

// LockExternalTransfer locks the transfer until the transaction ends and
// returns it.
func (s outboxStore) LockExternalTransfer(ctx context.Context, id transfer.ID) (application.ExternalTransfer, error) {
	query := selectFrom[externalTransferRow]("locking external transfer", externalTransfersTable).
		Where("id = @id", args{"id": uuid.UUID(id)}).
		ForUpdate()

	return readExternalTransfer(ctx, s.db, query)
}

// AdvanceStatus changes the status if the sequence is still the one seen:
// the compare-and-set.
func (s outboxStore) AdvanceStatus(ctx context.Context, change application.StatusChange, seenSeq int) error {
	advance := update("advancing status", externalTransfersTable).
		Set("status", string(change.To)).
		SetSQL(failureCodeSQL, args{"failure_code": change.FailureCode}).
		SetSQL("status_seq = status_seq + 1, updated_at = now()", nil).
		Where("id = @id AND status_seq = @seen_seq", args{"id": uuid.UUID(change.TransferID), "seen_seq": seenSeq})

	return advance.ExecOne(ctx, s.db, errStaleStatus)
}

// DeferOutbox reschedules the message if the token still owns it.
func (s outboxStore) DeferOutbox(ctx context.Context, claim application.OutboxClaim, delay time.Duration, reason string) (bool, error) {
	deferral := update("deferring outbox message", outboxMessagesTable).
		SetSQL("next_attempt_at = "+afterDelaySQL, args{"delay_ms": delay.Milliseconds()}).
		Set("last_error", reason).
		SetSQL("lease_owner = NULL, lease_expires_at = NULL", nil).
		Where(ownedByLeaseSQL, args{"id": claim.ID, "token": claim.Token})

	deferred, err := deferral.Exec(ctx, s.db)

	return deferred == 1, err
}

// InsertInbox records the event unless it is already there: the unique
// (source, event_id) of the inbox migration turns a redelivery into
// nothing.
func (s inboxStore) InsertInbox(ctx context.Context, event application.PartnerEvent, correlationID string) (bool, error) {
	row, err := inboxEventRowOf(event, correlationID)
	if err != nil {
		return false, err
	}

	inserted, err := insertRow("inserting inbox event", inboxEventsTable, row).
		OnConflictDoNothing("(source, event_id)").
		Exec(ctx, s.db)

	return inserted == 1, err
}

// ReclaimInbox locks a pending event no other transaction holds.
func (s inboxStore) ReclaimInbox(ctx context.Context, id int64) (bool, error) {
	var locked int64

	return scanOptionalRow(ctx, s.db, newStatement("reclaiming inbox event", reclaimInboxSQL, args{"id": id}), &locked)
}

// ClaimInbox returns the next event to apply, or false when none is ready.
func (s inboxStore) ClaimInbox(ctx context.Context) (application.InboxEvent, bool, error) {
	rows, err := queryRows(ctx, s.db, newStatement("claiming inbox event", claimInboxSQL), pgx.RowToStructByName[inboxClaimRow])
	if err != nil {
		return application.InboxEvent{}, false, err
	}

	if len(rows) == 0 {
		return application.InboxEvent{}, false, nil
	}

	event, err := rows[0].toDomain()

	return event, err == nil, err
}

// FinishInbox closes the event with its outcome.
func (s inboxStore) FinishInbox(ctx context.Context, id int64, status application.InboxStatus, outcome string) error {
	finish := update("finishing inbox event", inboxEventsTable).
		Set("status", string(status)).
		Set("outcome", outcome).
		SetSQL("processed_at = now()", nil).
		Where("id = @id AND status = 'pending'", args{"id": id})

	_, err := finish.Exec(ctx, s.db)

	return err
}

// DeferInbox puts the event back for a later attempt.
func (s inboxStore) DeferInbox(ctx context.Context, id int64, delay time.Duration, reason string) error {
	deferral := update("deferring inbox event", inboxEventsTable).
		SetSQL("next_attempt_at = "+afterDelaySQL, args{"delay_ms": delay.Milliseconds()}).
		Set("outcome", reason).
		Where("id = @id AND status = 'pending'", args{"id": id})

	_, err := deferral.Exec(ctx, s.db)

	return err
}
