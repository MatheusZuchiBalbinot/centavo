package postgres

import (
	"context"
	"errors"
	"fmt"
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

// InsertExternalTransfer records a new transfer in created.
func (s LedgerStore) InsertExternalTransfer(ctx context.Context, external application.ExternalTransfer) (time.Time, error) {
	var createdAt time.Time

	err := s.db.QueryRow(ctx, insertExternalTransferSQL, uuid.UUID(external.ID), external.EndToEndID, uuid.UUID(external.OperationID), external.ClientRef, uuid.UUID(external.AccountID), int64(external.Amount.Cents()), external.DestinationKey).Scan(&createdAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("inserting external transfer: %w", err)
	}

	return createdAt, nil
}

const insertTransitionSQL = `
INSERT INTO external_transfer_transitions (transfer_id, seq, from_status, to_status, cause, ledger_transaction_id, correlation_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)`

// InsertTransition appends one line to the transfer's state history.
func (s LedgerStore) InsertTransition(ctx context.Context, transition application.TransitionRecord) error {
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

	_, err := s.db.Exec(ctx, insertTransitionSQL, uuid.UUID(transition.TransferID), transition.Seq, from, string(transition.To), transition.Cause, ledgerTransactionID, transition.CorrelationID)
	if err != nil {
		return fmt.Errorf("inserting transition: %w", err)
	}

	return nil
}

const externalTransferColumns = `id, end_to_end_id, operation_id, client_ref, account_id, amount_cents, destination_key, status::text, status_seq, coalesce(failure_code, ''), created_at`

// FindExternalTransfer reads a transfer and its state history.
func (s LedgerStore) FindExternalTransfer(ctx context.Context, id transfer.ID) (application.ExternalTransfer, []application.TransitionRecord, error) {
	row := s.db.QueryRow(ctx, `SELECT `+externalTransferColumns+` FROM external_transfers WHERE id = $1`, uuid.UUID(id))

	external, err := scanExternalTransfer(row)
	if err != nil {
		return application.ExternalTransfer{}, nil, err
	}

	transitions, err := s.transitionsOf(ctx, id)

	return external, transitions, err
}

// FindExternalTransferByEndToEnd finds the transfer a partner event is about.
func (s LedgerStore) FindExternalTransferByEndToEnd(ctx context.Context, endToEndID string) (application.ExternalTransfer, error) {
	row := s.db.QueryRow(ctx, `SELECT `+externalTransferColumns+` FROM external_transfers WHERE end_to_end_id = $1`, endToEndID)

	return scanExternalTransfer(row)
}

func scanExternalTransfer(row pgx.Row) (application.ExternalTransfer, error) {
	var (
		external                   application.ExternalTransfer
		id, operationID, accountID uuid.UUID
		amountCents                int64
		status                     string
	)

	err := row.Scan(&id, &external.EndToEndID, &operationID, &external.ClientRef, &accountID, &amountCents, &external.DestinationKey, &status, &external.StatusSeq, &external.FailureCode, &external.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.ExternalTransfer{}, transfer.ErrTransferNotFound
	}

	if err != nil {
		return application.ExternalTransfer{}, fmt.Errorf("reading external transfer: %w", err)
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

const transitionsSQL = `
SELECT seq, from_status::text, to_status::text, cause, ledger_transaction_id, correlation_id, created_at
FROM external_transfer_transitions
WHERE transfer_id = $1
ORDER BY seq, id`

func (s LedgerStore) transitionsOf(ctx context.Context, id transfer.ID) ([]application.TransitionRecord, error) {
	rows, err := s.db.Query(ctx, transitionsSQL, uuid.UUID(id))
	if err != nil {
		return nil, fmt.Errorf("reading transitions: %w", err)
	}

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (application.TransitionRecord, error) {
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

		transition.TransferID = id
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
	})
}

const claimAttemptSQL = `
UPDATE external_transfers
SET end_to_end_id = $3, updated_at = now()
WHERE id = $1 AND end_to_end_id = $2 AND status = 'created'`

// ClaimAttempt is the naive relay's compare-and-set on the end_to_end_id.
func (s LedgerStore) ClaimAttempt(ctx context.Context, id transfer.ID, seen, next string) (bool, error) {
	tag, err := s.db.Exec(ctx, claimAttemptSQL, uuid.UUID(id), seen, next)
	if err != nil {
		return false, fmt.Errorf("claiming an attempt: %w", err)
	}

	return tag.RowsAffected() == 1, nil
}

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

// OverwriteStatus sets the status, whatever it was.
func (s LedgerStore) OverwriteStatus(ctx context.Context, change application.StatusChange) (transfer.Status, int, error) {
	var (
		previous string
		seq      int
	)

	err := s.db.QueryRow(ctx, overwriteStatusSQL, uuid.UUID(change.TransferID), string(change.To), change.FailureCode).Scan(&previous, &seq)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, transfer.ErrTransferNotFound
	}

	if err != nil {
		return "", 0, fmt.Errorf("overwriting status: %w", err)
	}

	return transfer.Status(previous), seq, nil
}

const insertIncomingSQL = `
INSERT INTO incoming_transfers (id, end_to_end_id, account_id, amount_cents, source_event_id)
VALUES ($1, $2, $3, $4, $5)`

// InsertIncoming records a PIX in; a second one for the same end_to_end_id
// is application.ErrAlreadyCredited.
func (s LedgerStore) InsertIncoming(ctx context.Context, incoming application.IncomingRecord) error {
	_, err := s.db.Exec(ctx, insertIncomingSQL, incoming.ID, incoming.EndToEndID, uuid.UUID(incoming.AccountID), int64(incoming.Amount.Cents()), incoming.SourceEventID)

	var pgErr *pgconn.PgError

	isDuplicate := errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
	if isDuplicate {
		return application.ErrAlreadyCredited
	}

	if err != nil {
		return fmt.Errorf("inserting incoming transfer: %w", err)
	}

	return nil
}

// Notify wakes the relays: NOTIFY pix_out with the transfer id. Nobody
// listening means nobody hears it.
func (s LedgerStore) Notify(ctx context.Context, channel, payload string) error {
	_, err := s.db.Exec(ctx, `SELECT pg_notify($1, $2)`, channel, payload)
	if err != nil {
		return fmt.Errorf("notifying %s: %w", channel, err)
	}

	return nil
}
