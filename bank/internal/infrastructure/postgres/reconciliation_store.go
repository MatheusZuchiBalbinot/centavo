package postgres

import (
	"context"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
)

const (
	reconciliationRunsTable   = "reconciliation_runs"
	reconciliationItemsTable  = "reconciliation_items"
	reconciliationCursorTable = "reconciliation_cursor"
)

// saveCursorSQL has no WHERE on purpose: reconciliation_cursor holds one
// row, and saving writes it whatever it was.
const saveCursorSQL = `UPDATE reconciliation_cursor SET last_partner_seq = @to`

const (
	countItemsSQL     = `SELECT count(*) FROM reconciliation_items WHERE end_to_end_id = @end_to_end_id AND partner_entry_kind = @kind`
	incomingExistsSQL = `SELECT EXISTS (SELECT 1 FROM incoming_transfers WHERE end_to_end_id = @end_to_end_id)`
)

// reconciliationRunRow is a batch as it starts.
type reconciliationRunRow struct {
	FromPartnerSeq int64 `db:"from_partner_seq"`
	ToPartnerSeq   int64 `db:"to_partner_seq"`
	FencingToken   int64 `db:"fencing_token"`
}

// reconciliationItemRow is how one statement entry was reconciled.
type reconciliationItemRow struct {
	RunID            int64  `db:"run_id"`
	PartnerSeq       int64  `db:"partner_seq"`
	EndToEndID       string `db:"end_to_end_id"`
	PartnerEntryKind string `db:"partner_entry_kind"`
	AmountCents      int64  `db:"amount_cents"`
	Divergence       string `db:"divergence"`
	Action           string `db:"action"`
}

// cursorRow is the one row of reconciliation_cursor.
type cursorRow struct {
	LastPartnerSeq int64 `db:"last_partner_seq"`
}

// reconciliationStore keeps reconciliation runs, items and the cursor.
type reconciliationStore struct {
	db querier
}

// ReadCursor returns the last statement entry reconciled.
func (s reconciliationStore) ReadCursor(ctx context.Context) (int64, error) {
	row, err := selectFrom[cursorRow]("reading the reconciliation cursor", reconciliationCursorTable).One(ctx, s.db)

	return row.LastPartnerSeq, err
}

// AdvanceCursor moves the cursor from where the batch started; zero rows
// means another reconciler booked it first.
func (s reconciliationStore) AdvanceCursor(ctx context.Context, from, to int64) (bool, error) {
	advanced, err := update("advancing the reconciliation cursor", reconciliationCursorTable).
		Set("last_partner_seq", to).
		Where("last_partner_seq = @from", args{"from": from}).
		Exec(ctx, s.db)

	return advanced == 1, err
}

// SaveCursor writes the cursor without looking at where it was.
func (s reconciliationStore) SaveCursor(ctx context.Context, to int64) error {
	_, err := execute(ctx, s.db, newStatement("saving the reconciliation cursor", saveCursorSQL, args{"to": to}))

	return err
}

// StartRun records a batch.
func (s reconciliationStore) StartRun(ctx context.Context, from, to, token int64) (int64, error) {
	var id int64

	run := reconciliationRunRow{FromPartnerSeq: from, ToPartnerSeq: to, FencingToken: token}
	err := insertRow("starting a reconciliation run", reconciliationRunsTable, run).Returning("id").Scan(ctx, s.db, &id)

	return id, err
}

// FinishRun marks the batch booked.
func (s reconciliationStore) FinishRun(ctx context.Context, runID int64) error {
	_, err := update("finishing a reconciliation run", reconciliationRunsTable).
		SetSQL("finished_at = now()", nil).
		Where("id = @id", args{"id": runID}).
		Exec(ctx, s.db)

	return err
}

// InsertItem records how one entry was reconciled.
func (s reconciliationStore) InsertItem(ctx context.Context, item application.ReconciliationItem) (int64, error) {
	var id int64

	row := reconciliationItemRow{
		RunID: item.RunID, PartnerSeq: item.PartnerSeq, EndToEndID: string(item.EndToEndID), PartnerEntryKind: string(item.EntryKind),
		AmountCents: int64(item.AmountCents), Divergence: string(item.Divergence), Action: string(item.Action),
	}
	err := insertRow("recording a reconciliation item", reconciliationItemsTable, row).Returning("id").Scan(ctx, s.db, &id)

	return id, err
}

// CountItems counts the entries of a kind already reconciled for an end_to_end_id.
func (s reconciliationStore) CountItems(ctx context.Context, endToEndID transfer.EndToEndID, kind application.StatementKind) (int, error) {
	var count int

	values := args{"end_to_end_id": string(endToEndID), "kind": string(kind)}
	err := scanRow(ctx, s.db, newStatement("counting reconciliation items", countItemsSQL, values), &count)

	return count, err
}

// IsIncomingCredited reports a PIX in already in incoming_transfers.
func (s reconciliationStore) IsIncomingCredited(ctx context.Context, endToEndID transfer.EndToEndID) (bool, error) {
	var isCredited bool

	values := args{"end_to_end_id": string(endToEndID)}
	err := scanRow(ctx, s.db, newStatement("looking up an incoming PIX", incomingExistsSQL, values), &isCredited)

	return isCredited, err
}
