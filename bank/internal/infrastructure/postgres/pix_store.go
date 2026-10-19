package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

// uniqueViolation is the error of a second row for the same key.
const uniqueViolation = "23505"

const insertExternalTransferSQL = `
INSERT INTO external_transfers (id, end_to_end_id, operation_id, client_ref, account_id, amount_cents, destination_key, status, status_seq)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'created', 1)
RETURNING created_at`

const insertTransitionSQL = `
INSERT INTO external_transfer_transitions (transfer_id, seq, from_status, to_status, cause, ledger_transaction_id, correlation_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

const externalTransferColumns = `id, end_to_end_id, operation_id, client_ref, account_id, amount_cents, destination_key,
	status::text, status_seq, coalesce(failure_code, ''), created_at`

const transitionsSQL = `
SELECT seq, from_status::text, to_status::text, cause, ledger_transaction_id, correlation_id, created_at
FROM external_transfer_transitions
WHERE transfer_id = $1
ORDER BY seq, id`

const claimAttemptSQL = `
UPDATE external_transfers
SET end_to_end_id = $3, updated_at = now()
WHERE id = $1 AND end_to_end_id = $2 AND status = 'created'`

// overwriteStatusSQL writes the status without checking it; the CTE reads
// the old one in the same statement so the history shows the jump.
const overwriteStatusSQL = `
WITH previous AS (SELECT status FROM external_transfers WHERE id = $1)
UPDATE external_transfers
SET status = $2::external_transfer_status,
    status_seq = status_seq + 1,
    failure_code = CASE WHEN $2::external_transfer_status = 'failed' THEN $3 ELSE NULL END,
    updated_at = now()
WHERE id = $1
RETURNING (SELECT status::text FROM previous), status_seq`

const insertIncomingSQL = `
INSERT INTO incoming_transfers (id, end_to_end_id, account_id, amount_cents, source_event_id)
VALUES ($1, $2, $3, $4, $5)`

const listCreatedSQL = `
SELECT id FROM external_transfers
WHERE status = 'created'
ORDER BY updated_at
LIMIT $1`

// lockEndToEndSQL is a transaction-scoped advisory lock keyed by the PIX:
// the reconciler and the inbox both credit incoming PIX, and the lock gives
// them one order to meet in.
const lockEndToEndSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`

const lockTransfersOfSQL = `SELECT id FROM external_transfers WHERE end_to_end_id = ANY($1) ORDER BY id FOR UPDATE`

const (
	findExternalTransferSQL           = `SELECT ` + externalTransferColumns + ` FROM external_transfers WHERE id = $1`
	findExternalTransferByEndToEndSQL = `SELECT ` + externalTransferColumns + ` FROM external_transfers WHERE end_to_end_id = $1`
	notifySQL                         = `SELECT pg_notify($1, $2)`
)

// pixStore keeps external transfers, their transitions and incoming PIX.
type pixStore struct {
	db querier
}

// InsertExternalTransfer records a new transfer in created.
func (s pixStore) InsertExternalTransfer(ctx context.Context, external application.ExternalTransfer) (time.Time, error) {
	var createdAt time.Time

	amountCents := int64(external.Amount.Cents())
	insert := newStatement(
		"inserting external transfer", insertExternalTransferSQL,
		uuid.UUID(external.ID), external.EndToEndID, uuid.UUID(external.OperationID), external.ClientRef,
		uuid.UUID(external.AccountID), amountCents, external.DestinationKey,
	)
	err := scanRow(ctx, s.db, insert, &createdAt)

	return createdAt, err
}

// InsertTransition appends one line to the transfer's state history.
func (s pixStore) InsertTransition(ctx context.Context, transition application.TransitionRecord) error {
	var from *string
	if transition.From != nil {
		status := string(*transition.From)
		from = &status
	}

	var ledgerTransactionID *uuid.UUID
	if transition.LedgerTransactionID != nil {
		id := uuid.UUID(*transition.LedgerTransactionID)
		ledgerTransactionID = &id
	}

	insert := newStatement(
		"inserting transition", insertTransitionSQL,
		uuid.UUID(transition.TransferID), transition.Seq, from, string(transition.To), transition.Cause,
		ledgerTransactionID, transition.CorrelationID,
	)
	_, err := execute(ctx, s.db, insert)

	return err
}

// FindExternalTransfer reads a transfer and its state history.
func (s pixStore) FindExternalTransfer(ctx context.Context, id transfer.ID) (application.ExternalTransfer, []application.TransitionRecord, error) {
	external, err := readExternalTransfer(ctx, s.db, newStatement("reading external transfer", findExternalTransferSQL, uuid.UUID(id)))
	if err != nil {
		return application.ExternalTransfer{}, nil, err
	}

	transitions, err := s.transitionsOf(ctx, id)

	return external, transitions, err
}

// FindExternalTransferByEndToEnd finds the transfer a partner event is about.
func (s pixStore) FindExternalTransferByEndToEnd(ctx context.Context, endToEndID transfer.EndToEndID) (application.ExternalTransfer, error) {
	return readExternalTransfer(ctx, s.db, newStatement("reading external transfer", findExternalTransferByEndToEndSQL, endToEndID))
}

// ClaimAttempt is the naive relay's compare-and-set on the end_to_end_id.
func (s pixStore) ClaimAttempt(ctx context.Context, id transfer.ID, seen, next transfer.EndToEndID) (bool, error) {
	claimed, err := execute(ctx, s.db, newStatement("claiming an attempt", claimAttemptSQL, uuid.UUID(id), seen, next))

	return claimed == 1, err
}

// OverwriteStatus sets the status, whatever it was.
func (s pixStore) OverwriteStatus(ctx context.Context, change application.StatusChange) (transfer.Status, int, error) {
	var (
		previous string
		seq      int
	)

	overwrite := newStatement("overwriting status", overwriteStatusSQL, uuid.UUID(change.TransferID), string(change.To), change.FailureCode)

	err := scanRow(ctx, s.db, overwrite, &previous, &seq)
	if err != nil {
		return "", 0, transferNotFoundOr(err)
	}

	return transfer.Status(previous), seq, nil
}

// InsertIncoming records a PIX in; a second one for the same end_to_end_id
// is application.ErrAlreadyCredited.
func (s pixStore) InsertIncoming(ctx context.Context, incoming application.IncomingRecord) error {
	amountCents := int64(incoming.Amount.Cents())
	insert := newStatement(
		"inserting incoming transfer", insertIncomingSQL,
		incoming.ID, incoming.EndToEndID, uuid.UUID(incoming.AccountID), amountCents, incoming.SourceEventID,
	)

	_, err := execute(ctx, s.db, insert)

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
	_, err := execute(ctx, s.db, newStatement("notifying "+channel, notifySQL, channel, payload))

	return err
}

// ListCreated returns transfers still waiting for the partner.
func (s pixStore) ListCreated(ctx context.Context, limit int) ([]transfer.ID, error) {
	return queryRows(ctx, s.db, newStatement("listing created transfers", listCreatedSQL, limit), scanTransferID)
}

// LockEndToEnd holds one PIX until the transaction ends.
func (s pixStore) LockEndToEnd(ctx context.Context, endToEndID transfer.EndToEndID) error {
	_, err := execute(ctx, s.db, newStatement("locking a PIX", lockEndToEndSQL, endToEndID))

	return err
}

// LockTransfersOf locks a batch of transfers in id order, which every other
// writer that takes more than one of them follows too.
func (s pixStore) LockTransfersOf(ctx context.Context, endToEndIDs []transfer.EndToEndID) error {
	_, err := execute(ctx, s.db, newStatement("locking transfers", lockTransfersOfSQL, endToEndIDs))

	return err
}

// readExternalTransfer reads the transfer a statement selects; a missing one
// is transfer.ErrTransferNotFound.
func readExternalTransfer(ctx context.Context, db querier, st statement) (application.ExternalTransfer, error) {
	var (
		external                   application.ExternalTransfer
		id, operationID, accountID uuid.UUID
		amountCents                int64
		status                     string
	)

	err := scanRow(
		ctx, db, st,
		&id, &external.EndToEndID, &operationID, &external.ClientRef, &accountID, &amountCents,
		&external.DestinationKey, &status, &external.StatusSeq, &external.FailureCode, &external.CreatedAt,
	)
	if err != nil {
		return application.ExternalTransfer{}, transferNotFoundOr(err)
	}

	amount, err := money.NewAmount(money.Cents(amountCents))
	if err != nil {
		return application.ExternalTransfer{}, err
	}

	external.ID = transfer.ID(id)
	external.OperationID = ledger.OperationID(operationID)
	external.AccountID = ledger.AccountID(accountID)
	external.Amount = amount
	external.Status = transfer.Status(status)

	return external, nil
}

func (s pixStore) transitionsOf(ctx context.Context, id transfer.ID) ([]application.TransitionRecord, error) {
	transitions, err := queryRows(ctx, s.db, newStatement("reading transitions", transitionsSQL, uuid.UUID(id)), scanTransition)
	if err != nil {
		return nil, err
	}

	for k := range transitions {
		transitions[k].TransferID = id
	}

	return transitions, nil
}

func scanTransition(row pgx.CollectableRow) (application.TransitionRecord, error) {
	var (
		transition    application.TransitionRecord
		from          *string
		to            string
		ledgerTransID *uuid.UUID
	)

	err := row.Scan(&transition.Seq, &from, &to, &transition.Cause, &ledgerTransID, &transition.CorrelationID, &transition.CreatedAt)
	if err != nil {
		return application.TransitionRecord{}, err
	}

	transition.To = transfer.Status(to)

	if from != nil {
		status := transfer.Status(*from)
		transition.From = &status
	}

	if ledgerTransID != nil {
		transactionID := ledger.TransactionID(*ledgerTransID)
		transition.LedgerTransactionID = &transactionID
	}

	return transition, nil
}

func scanTransferID(row pgx.CollectableRow) (transfer.ID, error) {
	var id uuid.UUID
	err := row.Scan(&id)

	return transfer.ID(id), err
}

// transferNotFoundOr turns a missing row into the domain error.
func transferNotFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return transfer.ErrTransferNotFound
	}

	return err
}
