// Package export reads the part of the bank's ledger a round touched, with
// the read-only bank_auditor role, in one REPEATABLE READ snapshot.
package export

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/lab/internal/snapshot"
)

// Request says which round to export and which accounts it created.
type Request struct {
	Round      string
	AccountIDs []string
}

// The queries read the tables raw: the checker recomputes everything from
// entries and never trusts the bank's own aggregates.
const (
	accountsSQL = `
SELECT id::text, kind::text, coalesce(system_code, '')
FROM accounts
WHERE kind = 'system' OR id = ANY($1::uuid[])
ORDER BY id`

	balancesSQL = `
SELECT b.account_id::text, b.balance_cents, b.last_entry_seq
FROM account_balances b
JOIN accounts a ON a.id = b.account_id
WHERE a.kind = 'system' OR a.id = ANY($1::uuid[])
ORDER BY b.account_id`

	// The round's PIX out are the transfers its clients created; left()
	// instead of LIKE, because a round id never needs escaping this way.
	roundTransfersSQL = `
SELECT id FROM external_transfers WHERE left(client_ref, length($1::text) + 1) = $1::text || '-'`

	// A round's ledger transactions carry its client_refs, or belong to its
	// PIX out (settlement, refund, return) or to a PIX in of its accounts.
	transactionsSQL = `
SELECT id::text, kind::text, coalesce(client_ref, ''), correlation_id,
       coalesce(external_transfer_id::text, ''), coalesce(incoming_transfer_id::text, '')
FROM ledger_transactions
WHERE left(client_ref, length($1::text) + 1) = $1::text || '-'
   OR external_transfer_id IN (` + roundTransfersSQL + `)
   OR incoming_transfer_id IN (SELECT id FROM incoming_transfers WHERE account_id = ANY($2::uuid[]))
ORDER BY id`

	entriesSQL = `
SELECT e.id, e.transaction_id::text, e.account_id::text, e.amount_cents, e.account_seq, e.balance_after_cents
FROM ledger_entries e
JOIN accounts a ON a.id = e.account_id
LEFT JOIN ledger_transactions t ON t.id = e.transaction_id
WHERE a.kind = 'system' OR a.id = ANY($1::uuid[]) OR left(t.client_ref, length($2::text) + 1) = $2::text || '-'
ORDER BY e.id`

	transfersSQL = `
SELECT id::text, end_to_end_id, client_ref, account_id::text, amount_cents, status::text, status_seq, coalesce(failure_code, '')
FROM external_transfers
WHERE id IN (` + roundTransfersSQL + `)
ORDER BY id`

	transitionsSQL = `
SELECT transfer_id::text, seq, coalesce(from_status::text, ''), to_status::text, cause, coalesce(ledger_transaction_id::text, '')
FROM external_transfer_transitions
WHERE transfer_id IN (` + roundTransfersSQL + `)
ORDER BY transfer_id, seq, id`

	incomingSQL = `
SELECT id::text, end_to_end_id, account_id::text, amount_cents
FROM incoming_transfers
WHERE account_id = ANY($1::uuid[])
ORDER BY id`
)

// Take reads the snapshot. Every query runs in the same read-only
// transaction, so the tables agree with each other.
func Take(ctx context.Context, pool *pgxpool.Pool, request Request) (snapshot.Snapshot, error) {
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

	tx, err := pool.BeginTx(ctx, options)
	if err != nil {
		return snapshot.Snapshot{}, fmt.Errorf("starting export transaction: %w", err)
	}

	// Read-only: rolling back is how the snapshot ends.
	defer func() { _ = tx.Rollback(ctx) }()

	s := snapshot.Snapshot{Meta: snapshot.Meta{Round: request.Round, TakenAt: time.Now().UTC()}}

	s.Accounts, err = collect(ctx, tx, accountsSQL, scanAccount, request.AccountIDs)
	if err != nil {
		return snapshot.Snapshot{}, err
	}

	s.Balances, err = collect(ctx, tx, balancesSQL, scanBalance, request.AccountIDs)
	if err != nil {
		return snapshot.Snapshot{}, err
	}

	s.Transactions, err = collect(ctx, tx, transactionsSQL, scanTransaction, request.Round, request.AccountIDs)
	if err != nil {
		return snapshot.Snapshot{}, err
	}

	s.Transfers, err = collect(ctx, tx, transfersSQL, scanTransfer, request.Round)
	if err != nil {
		return snapshot.Snapshot{}, err
	}

	s.Transitions, err = collect(ctx, tx, transitionsSQL, scanTransition, request.Round)
	if err != nil {
		return snapshot.Snapshot{}, err
	}

	s.Incoming, err = collect(ctx, tx, incomingSQL, scanIncoming, request.AccountIDs)
	if err != nil {
		return snapshot.Snapshot{}, err
	}

	s.Entries, err = collect(ctx, tx, entriesSQL, scanEntry, request.AccountIDs, request.Round)
	if err != nil {
		return snapshot.Snapshot{}, err
	}

	return s, nil
}

func collect[T any](ctx context.Context, tx pgx.Tx, sql string, scan pgx.RowToFunc[T], args ...any) ([]T, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("exporting: %w", err)
	}

	collected, err := pgx.CollectRows(rows, scan)
	if err != nil {
		return nil, fmt.Errorf("exporting: %w", err)
	}

	return collected, nil
}

func scanAccount(row pgx.CollectableRow) (snapshot.Account, error) {
	var a snapshot.Account
	err := row.Scan(&a.ID, &a.Kind, &a.SystemCode)

	return a, err
}

func scanBalance(row pgx.CollectableRow) (snapshot.Balance, error) {
	var b snapshot.Balance
	err := row.Scan(&b.AccountID, &b.BalanceCents, &b.LastEntrySeq)

	return b, err
}

func scanTransaction(row pgx.CollectableRow) (snapshot.Transaction, error) {
	var t snapshot.Transaction
	err := row.Scan(&t.ID, &t.Kind, &t.ClientRef, &t.CorrelationID, &t.ExternalTransferID, &t.IncomingTransferID)

	return t, err
}

func scanTransfer(row pgx.CollectableRow) (snapshot.ExternalTransfer, error) {
	var t snapshot.ExternalTransfer
	err := row.Scan(&t.ID, &t.EndToEndID, &t.ClientRef, &t.AccountID, &t.AmountCents, &t.Status, &t.StatusSeq, &t.FailureCode)

	return t, err
}

func scanTransition(row pgx.CollectableRow) (snapshot.Transition, error) {
	var t snapshot.Transition
	err := row.Scan(&t.TransferID, &t.Seq, &t.From, &t.To, &t.Cause, &t.LedgerTransactionID)

	return t, err
}

func scanIncoming(row pgx.CollectableRow) (snapshot.Incoming, error) {
	var i snapshot.Incoming
	err := row.Scan(&i.ID, &i.EndToEndID, &i.AccountID, &i.AmountCents)

	return i, err
}

func scanEntry(row pgx.CollectableRow) (snapshot.Entry, error) {
	var e snapshot.Entry
	err := row.Scan(&e.ID, &e.TransactionID, &e.AccountID, &e.AmountCents, &e.AccountSeq, &e.BalanceAfterCents)

	return e, err
}

// inFlightSQL also counts the partner events the bank took in and has not
// applied yet: with the inbox (D5) a webhook is acknowledged before its
// effect, and a snapshot taken in between would miss it.
const inFlightSQL = `
SELECT (SELECT count(*) FROM external_transfers
        WHERE status IN ('created', 'sent') AND id IN (` + roundTransfersSQL + `))
     + (SELECT count(*) FROM inbox_events WHERE status = 'pending')`

// InFlight counts what still moves the round's sagas: transfers not yet in a
// final status and events waiting in the inbox, for the quiescence of the cure.
func InFlight(ctx context.Context, pool *pgxpool.Pool, round string) (int, error) {
	var count int

	err := pool.QueryRow(ctx, inFlightSQL, round).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting transfers in flight: %w", err)
	}

	return count, nil
}

const roundTransferIDsSQL = `SELECT id::text FROM external_transfers WHERE id IN (` + roundTransfersSQL + `) ORDER BY id`

// TransferIDs lists the round's transfers, for the final reads.
func TransferIDs(ctx context.Context, pool *pgxpool.Pool, round string) ([]string, error) {
	rows, err := pool.Query(ctx, roundTransferIDsSQL, round)
	if err != nil {
		return nil, fmt.Errorf("listing the round's transfers: %w", err)
	}

	return pgx.CollectRows(rows, pgx.RowTo[string])
}
