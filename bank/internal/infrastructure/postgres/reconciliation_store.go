package postgres

import (
	"context"

	"centavo/bank/internal/application"

	"centavo/bank/internal/domain/transfer"
)

const startRunSQL = `
INSERT INTO reconciliation_runs (from_partner_seq, to_partner_seq, fencing_token)
VALUES ($1, $2, $3)
RETURNING id`

const insertItemSQL = `
INSERT INTO reconciliation_items (run_id, partner_seq, end_to_end_id, partner_entry_kind, amount_cents, divergence, action)
VALUES ($1, $2, $3, $4, $5, $6::divergence_kind, $7)
RETURNING id`

const (
	readCursorSQL     = `SELECT last_partner_seq FROM reconciliation_cursor`
	advanceCursorSQL  = `UPDATE reconciliation_cursor SET last_partner_seq = $2 WHERE last_partner_seq = $1`
	saveCursorSQL     = `UPDATE reconciliation_cursor SET last_partner_seq = $1`
	finishRunSQL      = `UPDATE reconciliation_runs SET finished_at = now() WHERE id = $1`
	countItemsSQL     = `SELECT count(*) FROM reconciliation_items WHERE end_to_end_id = $1 AND partner_entry_kind = $2`
	incomingExistsSQL = `SELECT EXISTS (SELECT 1 FROM incoming_transfers WHERE end_to_end_id = $1)`
)

// reconciliationStore keeps reconciliation runs, items and the cursor.
type reconciliationStore struct {
	db querier
}

// ReadCursor returns the last statement entry reconciled.
func (s reconciliationStore) ReadCursor(ctx context.Context) (int64, error) {
	var seq int64

	err := scanRow(ctx, s.db, newStatement("reading the reconciliation cursor", readCursorSQL), &seq)

	return seq, err
}

// AdvanceCursor moves the cursor from where the batch started; zero rows
// means another reconciler booked it first.
func (s reconciliationStore) AdvanceCursor(ctx context.Context, from, to int64) (bool, error) {
	advanced, err := execute(ctx, s.db, newStatement("advancing the reconciliation cursor", advanceCursorSQL, from, to))

	return advanced == 1, err
}

// SaveCursor writes the cursor without looking at where it was.
func (s reconciliationStore) SaveCursor(ctx context.Context, to int64) error {
	_, err := execute(ctx, s.db, newStatement("saving the reconciliation cursor", saveCursorSQL, to))

	return err
}

// StartRun records a batch.
func (s reconciliationStore) StartRun(ctx context.Context, from, to, token int64) (int64, error) {
	var id int64

	err := scanRow(ctx, s.db, newStatement("starting a reconciliation run", startRunSQL, from, to, token), &id)

	return id, err
}

// FinishRun marks the batch booked.
func (s reconciliationStore) FinishRun(ctx context.Context, runID int64) error {
	_, err := execute(ctx, s.db, newStatement("finishing a reconciliation run", finishRunSQL, runID))

	return err
}

// InsertItem records how one entry was reconciled.
func (s reconciliationStore) InsertItem(ctx context.Context, item application.ReconciliationItem) (int64, error) {
	var id int64

	insert := newStatement(
		"recording a reconciliation item", insertItemSQL,
		item.RunID, item.PartnerSeq, item.EndToEndID, string(item.EntryKind), int64(item.AmountCents), string(item.Divergence), item.Action,
	)
	err := scanRow(ctx, s.db, insert, &id)

	return id, err
}

// CountItems counts the entries of a kind already reconciled for an end_to_end_id.
func (s reconciliationStore) CountItems(ctx context.Context, endToEndID transfer.EndToEndID, kind application.StatementKind) (int, error) {
	var count int

	err := scanRow(ctx, s.db, newStatement("counting reconciliation items", countItemsSQL, endToEndID, string(kind)), &count)

	return count, err
}

// IsIncomingCredited reports a PIX in already in incoming_transfers.
func (s reconciliationStore) IsIncomingCredited(ctx context.Context, endToEndID transfer.EndToEndID) (bool, error) {
	var isCredited bool

	err := scanRow(ctx, s.db, newStatement("looking up an incoming PIX", incomingExistsSQL, endToEndID), &isCredited)

	return isCredited, err
}
