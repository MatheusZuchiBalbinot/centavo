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

	// left() instead of LIKE: a round id never needs escaping this way.
	transactionsSQL = `
SELECT id::text, kind::text, coalesce(client_ref, ''), correlation_id
FROM ledger_transactions
WHERE left(client_ref, length($1::text) + 1) = $1::text || '-'
ORDER BY id`

	entriesSQL = `
SELECT e.id, e.transaction_id::text, e.account_id::text, e.amount_cents, e.account_seq, e.balance_after_cents
FROM ledger_entries e
JOIN accounts a ON a.id = e.account_id
LEFT JOIN ledger_transactions t ON t.id = e.transaction_id
WHERE a.kind = 'system' OR a.id = ANY($1::uuid[]) OR left(t.client_ref, length($2::text) + 1) = $2::text || '-'
ORDER BY e.id`
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

	s.Transactions, err = collect(ctx, tx, transactionsSQL, scanTransaction, request.Round)
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
	err := row.Scan(&t.ID, &t.Kind, &t.ClientRef, &t.CorrelationID)

	return t, err
}

func scanEntry(row pgx.CollectableRow) (snapshot.Entry, error) {
	var e snapshot.Entry
	err := row.Scan(&e.ID, &e.TransactionID, &e.AccountID, &e.AmountCents, &e.AccountSeq, &e.BalanceAfterCents)

	return e, err
}
