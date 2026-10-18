package postgres

import (
	"context"
	"fmt"

	"centavo/bank/internal/application"

	"centavo/bank/internal/domain/transfer"
)

// reconciliationStore keeps reconciliation runs, items and the cursor.
type reconciliationStore struct {
	db querier
}

// ReadCursor returns the last statement entry reconciled.
func (s reconciliationStore) ReadCursor(ctx context.Context) (int64, error) {
	var seq int64

	err := s.db.QueryRow(ctx, `SELECT last_partner_seq FROM reconciliation_cursor`).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("reading the reconciliation cursor: %w", err)
	}

	return seq, nil
}

// AdvanceCursor moves the cursor from where the batch started; zero rows
// means another reconciler booked it first.
func (s reconciliationStore) AdvanceCursor(ctx context.Context, from, to int64) (bool, error) {
	tag, err := s.db.Exec(ctx, `UPDATE reconciliation_cursor SET last_partner_seq = $2 WHERE last_partner_seq = $1`, from, to)
	if err != nil {
		return false, fmt.Errorf("advancing the reconciliation cursor: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

// SaveCursor writes the cursor without looking at where it was.
func (s reconciliationStore) SaveCursor(ctx context.Context, to int64) error {
	_, err := s.db.Exec(ctx, `UPDATE reconciliation_cursor SET last_partner_seq = $1`, to)
	if err != nil {
		return fmt.Errorf("saving the reconciliation cursor: %w", err)
	}

	return nil
}

const startRunSQL = `
INSERT INTO reconciliation_runs (from_partner_seq, to_partner_seq, fencing_token)
VALUES ($1, $2, $3)
RETURNING id`

// StartRun records a batch.
func (s reconciliationStore) StartRun(ctx context.Context, from, to, token int64) (int64, error) {
	var id int64

	row := s.db.QueryRow(ctx, startRunSQL, from, to, token)

	err := row.Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("starting a reconciliation run: %w", err)
	}

	return id, nil
}

// FinishRun marks the batch booked.
func (s reconciliationStore) FinishRun(ctx context.Context, runID int64) error {
	_, err := s.db.Exec(ctx, `UPDATE reconciliation_runs SET finished_at = now() WHERE id = $1`, runID)
	if err != nil {
		return fmt.Errorf("finishing a reconciliation run: %w", err)
	}

	return nil
}

const insertItemSQL = `
INSERT INTO reconciliation_items (run_id, partner_seq, end_to_end_id, partner_entry_kind, amount_cents, divergence, action)
VALUES ($1, $2, $3, $4, $5, $6::divergence_kind, $7)
RETURNING id`

// InsertItem records how one entry was reconciled.
func (s reconciliationStore) InsertItem(ctx context.Context, item application.ReconciliationItem) (int64, error) {
	var id int64

	row := s.db.QueryRow(
		ctx,
		insertItemSQL,
		item.RunID,
		item.PartnerSeq,
		item.EndToEndID,
		string(item.EntryKind),
		int64(item.AmountCents),
		string(item.Divergence),
		item.Action,
	)

	err := row.Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("recording a reconciliation item: %w", err)
	}

	return id, nil
}

// CountItems counts the entries of a kind already reconciled for an end_to_end_id.
func (s reconciliationStore) CountItems(ctx context.Context, endToEndID transfer.EndToEndID, kind application.StatementKind) (int, error) {
	var count int

	row := s.db.QueryRow(
		ctx,
		`SELECT count(*) FROM reconciliation_items WHERE end_to_end_id = $1 AND partner_entry_kind = $2`,
		endToEndID,
		string(kind),
	)

	err := row.Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting reconciliation items: %w", err)
	}

	return count, nil
}

// IsIncomingCredited reports a PIX in already in incoming_transfers.
func (s reconciliationStore) IsIncomingCredited(ctx context.Context, endToEndID transfer.EndToEndID) (bool, error) {
	var isCredited bool

	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM incoming_transfers WHERE end_to_end_id = $1)`, endToEndID).Scan(&isCredited)
	if err != nil {
		return false, fmt.Errorf("looking up an incoming PIX: %w", err)
	}

	return isCredited, nil
}
