package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
)

// uniqueViolation is the error of a second row for the same key.
const uniqueViolation = "23505"

// overwriteStatusSQL writes the status without checking it; the CTE reads
// the old one in the same statement so the history shows the jump.
const overwriteStatusSQL = `
WITH previous AS (SELECT status FROM external_transfers WHERE id = @id)
UPDATE external_transfers
SET status = @status::external_transfer_status,
    status_seq = status_seq + 1,
    failure_code = CASE WHEN @status::external_transfer_status = 'failed' THEN @failure_code ELSE NULL END,
    updated_at = now()
WHERE id = @id
RETURNING (SELECT status::text FROM previous), status_seq`

// lockEndToEndSQL is a transaction-scoped advisory lock keyed by the PIX:
// the reconciler and the inbox both credit incoming PIX, and the lock gives
// them one order to meet in.
const lockEndToEndSQL = `SELECT pg_advisory_xact_lock(hashtextextended(@end_to_end_id, 0))`

const notifySQL = `SELECT pg_notify(@channel, @payload)`

// transferIDRow reads only a transfer's id.
type transferIDRow struct {
	ID uuid.UUID `db:"id"`
}

// pixStore keeps external transfers, their transitions and incoming PIX.
type pixStore struct {
	db querier
}

// InsertExternalTransfer records a new transfer in created.
func (s pixStore) InsertExternalTransfer(ctx context.Context, external application.ExternalTransfer) (time.Time, error) {
	var createdAt time.Time

	insert := insertRow("inserting external transfer", externalTransfersTable, newExternalTransferRowOf(external))
	err := insert.Returning("created_at").Scan(ctx, s.db, &createdAt)

	return createdAt, err
}

// InsertTransition appends one line to the transfer's state history.
func (s pixStore) InsertTransition(ctx context.Context, transition application.TransitionRecord) error {
	_, err := insertRow("inserting transition", transfersTransitionTable, transitionRowOf(transition)).Exec(ctx, s.db)

	return err
}

// FindExternalTransfer reads a transfer and its state history.
func (s pixStore) FindExternalTransfer(ctx context.Context, id transfer.ID) (application.ExternalTransfer, []application.TransitionRecord, error) {
	query := selectFrom[externalTransferRow]("reading external transfer", externalTransfersTable).
		Where("id = @id", args{"id": uuid.UUID(id)})

	external, err := readExternalTransfer(ctx, s.db, query)
	if err != nil {
		return application.ExternalTransfer{}, nil, err
	}

	transitions, err := s.transitionsOf(ctx, id)

	return external, transitions, err
}

// FindExternalTransferByEndToEnd finds the transfer a partner event is about.
func (s pixStore) FindExternalTransferByEndToEnd(ctx context.Context, endToEndID transfer.EndToEndID) (application.ExternalTransfer, error) {
	query := selectFrom[externalTransferRow]("reading external transfer", externalTransfersTable).
		Where("end_to_end_id = @end_to_end_id", args{"end_to_end_id": string(endToEndID)})

	return readExternalTransfer(ctx, s.db, query)
}

// ClaimAttempt is the naive relay's compare-and-set on the end_to_end_id.
func (s pixStore) ClaimAttempt(ctx context.Context, id transfer.ID, seen, next transfer.EndToEndID) (bool, error) {
	claim := update("claiming an attempt", externalTransfersTable).
		Set("end_to_end_id", string(next)).
		SetSQL("updated_at = now()", nil).
		Where("id = @id AND end_to_end_id = @seen AND status = 'created'", args{"id": uuid.UUID(id), "seen": string(seen)})

	claimed, err := claim.Exec(ctx, s.db)

	return claimed == 1, err
}

// OverwriteStatus sets the status, whatever it was.
func (s pixStore) OverwriteStatus(ctx context.Context, change application.StatusChange) (transfer.Status, int, error) {
	var (
		previous string
		seq      int
	)

	values := args{"id": uuid.UUID(change.TransferID), "status": string(change.To), "failure_code": change.FailureCode}

	err := scanRow(ctx, s.db, newStatement("overwriting status", overwriteStatusSQL, values), &previous, &seq)
	if err != nil {
		return "", 0, transferNotFoundOr(err)
	}

	return transfer.Status(previous), seq, nil
}

// InsertIncoming records a PIX in; a second one for the same end_to_end_id
// is application.ErrAlreadyCredited.
func (s pixStore) InsertIncoming(ctx context.Context, incoming application.IncomingRecord) error {
	_, err := insertRow("inserting incoming transfer", incomingTransfersTable, incomingTransferRowOf(incoming)).Exec(ctx, s.db)

	var pgErr *pgconn.PgError

	isDuplicate := errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
	if isDuplicate {
		return application.ErrAlreadyCredited
	}

	return err
}

// Notify wakes the relays: NOTIFY pix_out with the transfer id. Nobody
// listening means nobody hears it.
func (s pixStore) Notify(ctx context.Context, channel, payload string) error {
	_, err := execute(ctx, s.db, newStatement("notifying "+channel, notifySQL, args{"channel": channel, "payload": payload}))

	return err
}

// ListCreated returns transfers still waiting for the partner.
func (s pixStore) ListCreated(ctx context.Context, limit int) ([]transfer.ID, error) {
	rows, err := selectFrom[transferIDRow]("listing created transfers", externalTransfersTable).
		Where("status = 'created'", nil).
		OrderBy("updated_at").
		Limit(limit).
		All(ctx, s.db)
	if err != nil {
		return nil, err
	}

	ids := make([]transfer.ID, len(rows))

	for k, row := range rows {
		ids[k] = transfer.ID(row.ID)
	}

	return ids, nil
}

// LockEndToEnd holds one PIX until the transaction ends.
func (s pixStore) LockEndToEnd(ctx context.Context, endToEndID transfer.EndToEndID) error {
	lock := newStatement("locking a PIX", lockEndToEndSQL, args{"end_to_end_id": string(endToEndID)})
	_, err := execute(ctx, s.db, lock)

	return err
}

// LockTransfersOf locks a batch of transfers in id order, which every other
// writer that takes more than one of them follows too.
func (s pixStore) LockTransfersOf(ctx context.Context, endToEndIDs []transfer.EndToEndID) error {
	ids := make([]string, len(endToEndIDs))

	for k, id := range endToEndIDs {
		ids[k] = string(id)
	}

	_, err := selectFrom[transferIDRow]("locking transfers", externalTransfersTable).
		Where("end_to_end_id = ANY(@end_to_end_ids)", args{"end_to_end_ids": ids}).
		OrderBy("id").
		ForUpdate().
		All(ctx, s.db)

	return err
}

// readExternalTransfer reads the transfer a query selects; a missing one is
// transfer.ErrTransferNotFound.
func readExternalTransfer(ctx context.Context, db querier, query selectQuery[externalTransferRow]) (application.ExternalTransfer, error) {
	row, err := query.One(ctx, db)
	if err != nil {
		return application.ExternalTransfer{}, transferNotFoundOr(err)
	}

	return row.toDomain()
}

func (s pixStore) transitionsOf(ctx context.Context, id transfer.ID) ([]application.TransitionRecord, error) {
	rows, err := selectFrom[transitionReadRow]("reading transitions", transfersTransitionTable).
		Where("transfer_id = @transfer_id", args{"transfer_id": uuid.UUID(id)}).
		OrderBy("seq, id").
		All(ctx, s.db)
	if err != nil {
		return nil, err
	}

	transitions := make([]application.TransitionRecord, len(rows))

	for k, row := range rows {
		transitions[k] = row.toDomain()
	}

	return transitions, nil
}

// transferNotFoundOr turns a missing row into the domain error.
func transferNotFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer.ErrTransferNotFound
	}

	return err
}
