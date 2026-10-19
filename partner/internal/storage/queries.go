package storage

import (
	"context"
	"database/sql"
	"time"

	"centavo/partner/internal/truth"
)

const recordAttemptSQL = `UPDATE deliveries SET attempts = attempts + 1, next_attempt_at = ?, status = ? WHERE id = ?`

const (
	dueTransfersSQL = `
SELECT end_to_end_id FROM transfers
WHERE (status = 'processing' AND settle_at <= ?) OR (status = 'settled' AND return_at IS NOT NULL AND return_at <= ?)
ORDER BY settle_at LIMIT ?`
	markDeliveredSQL    = `UPDATE deliveries SET status = 'delivered', attempts = attempts + 1 WHERE id = ?`
	pendingCountSQL     = `SELECT count(*) FROM deliveries WHERE status = 'pending'`
	lastStatementSeqSQL = `SELECT coalesce(max(seq), 0) FROM statement_entries WHERE participant_id = ?`
)

// Get returns the authoritative state of a transfer.
func (d *DB) Get(ctx context.Context, endToEndID string) (truth.Transfer, error) {
	return findTransfer(ctx, d.db, endToEndID)
}

// Due lists transfers waiting for a decision: processing ones whose settle
// time came, and settled ones whose return time came.
func (d *DB) Due(ctx context.Context, now time.Time, limit int) ([]truth.Transfer, error) {
	list := newStatement("listing due transfers", dueTransfersSQL, now.UnixNano(), now.UnixNano(), limit)

	ids, err := queryRows(ctx, d.db, list, scanString)
	if err != nil {
		return nil, err
	}

	transfers := make([]truth.Transfer, 0, len(ids))

	for _, id := range ids {
		transfer, err := d.Get(ctx, id)
		if err != nil {
			return nil, err
		}

		transfers = append(transfers, transfer)
	}

	return transfers, nil
}

// Statement returns the participant's entries after a sequence number.
func (d *DB) Statement(ctx context.Context, participantID string, afterSeq int64, limit int) ([]truth.StatementEntry, error) {
	return queryRows(ctx, d.db, newStatement("reading the statement", statementSQL, participantID, afterSeq, limit), scanStatementEntry)
}

// DueDeliveries lists pending deliveries whose time came.
func (d *DB) DueDeliveries(ctx context.Context, now time.Time, limit int) ([]truth.Delivery, error) {
	query := newStatement("listing due deliveries", dueDeliveriesSQL, now.UnixNano(), limit)
	return queryRows(ctx, d.db, query, scanDelivery)
}

// MarkDelivered closes a delivery.
func (d *DB) MarkDelivered(ctx context.Context, id int64) error {
	_, err := execute(ctx, d.db, newStatement("closing a delivery", markDeliveredSQL, id))

	return err
}

// Reschedule records a failed attempt and when to try again.
func (d *DB) Reschedule(ctx context.Context, id int64, next time.Time) error {
	return d.recordAttempt(ctx, id, next, truth.DeliveryPending)
}

// Abandon records the last failed attempt: the delivery is given up, like a
// real provider does after its retries.
func (d *DB) Abandon(ctx context.Context, id int64, at time.Time) error {
	return d.recordAttempt(ctx, id, at, truth.DeliveryAbandoned)
}

// PendingDeliveries counts what is still to be sent, for the lab's quiescence.
func (d *DB) PendingDeliveries(ctx context.Context) (int, error) {
	var count int

	err := scanRow(ctx, d.db, newStatement("counting pending deliveries", pendingCountSQL), &count)

	return count, err
}

// LastStatementSeq is the participant's newest statement entry, or zero:
// the lab's quiescence waits until the bank reconciled up to it.
func (d *DB) LastStatementSeq(ctx context.Context, participantID string) (int64, error) {
	var seq int64

	err := scanRow(ctx, d.db, newStatement("reading the last statement entry", lastStatementSeqSQL, participantID), &seq)

	return seq, err
}

func (d *DB) recordAttempt(ctx context.Context, id int64, next time.Time, status truth.DeliveryStatus) error {
	query := newStatement("recording a delivery attempt", recordAttemptSQL, next.UnixNano(), status, id)
	_, err := execute(ctx, d.db, query)

	return err
}

func findTransfer(ctx context.Context, db querier, endToEndID string) (truth.Transfer, error) {
	var (
		transfer          truth.Transfer
		settleAt, created int64
		returnAt          sql.NullInt64
	)

	err := scanRow(
		ctx, db, newStatement("reading a transfer", selectTransferSQL, endToEndID),
		&transfer.EndToEndID, &transfer.ParticipantID, &transfer.AmountCents, &transfer.DestinationKey, &transfer.Status,
		&transfer.RejectReason, &transfer.IsDoubleProcessed, &settleAt, &returnAt, &created, &transfer.CorrelationID,
	)
	if err != nil {
		return truth.Transfer{}, notFoundOr(err, truth.ErrNotFound)
	}

	transfer.SettleAt = time.Unix(0, settleAt).UTC()
	transfer.CreatedAt = time.Unix(0, created).UTC()

	if returnAt.Valid {
		at := time.Unix(0, returnAt.Int64).UTC()
		transfer.ReturnAt = &at
	}

	return transfer, nil
}

func scanString(rows *sql.Rows) (string, error) {
	var value string
	err := rows.Scan(&value)

	return value, err
}

func scanStatementEntry(rows *sql.Rows) (truth.StatementEntry, error) {
	var (
		entry   truth.StatementEntry
		created int64
	)

	err := rows.Scan(
		&entry.Seq, &entry.ParticipantID, &entry.Kind, &entry.EndToEndID, &entry.AmountCents, &created,
		&entry.DestinationAccountID, &entry.CorrelationID,
	)
	entry.CreatedAt = time.Unix(0, created).UTC()

	return entry, err
}

func scanDelivery(rows *sql.Rows) (truth.Delivery, error) {
	var (
		delivery truth.Delivery
		next     int64
	)

	err := rows.Scan(&delivery.ID, &delivery.EventID, &delivery.ParticipantID, &delivery.Payload, &delivery.Attempts, &next, &delivery.CorrelationID)
	delivery.NextAttemptAt = time.Unix(0, next).UTC()
	delivery.Status = truth.DeliveryPending

	return delivery, err
}
