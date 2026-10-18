package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

// querier is what both a pool and a transaction offer: on a pool every
// statement is its own autocommit, inside a transaction they commit together.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// LedgerStore implements the account, position and ledger interfaces of the
// application layer. Every method is a single statement; whether statements
// commit together depends on the querier it was built on.
type LedgerStore struct {
	db querier
}

// NewLedgerStore runs every statement in its own autocommit on the pool.
func NewLedgerStore(pool *pgxpool.Pool) LedgerStore {
	return LedgerStore{db: pool}
}

const lockPositionsSQL = `
SELECT account_id
FROM account_balances
WHERE account_id = ANY($1::uuid[])
ORDER BY account_id
FOR UPDATE`

// LockPositions takes the row locks of the accounts' balances in id order,
// so two transfers in opposite directions never wait for each other in a
// cycle (docs/domain.md, section 6). The locks last until the transaction ends.
func (s LedgerStore) LockPositions(ctx context.Context, ids []ledger.AccountID) error {
	uuids := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		uuids = append(uuids, uuid.UUID(id))
	}

	rows, err := s.db.Query(ctx, lockPositionsSQL, uuids)
	if err != nil {
		return fmt.Errorf("locking positions: %w", err)
	}

	rows.Close()

	err = rows.Err()
	if err != nil {
		return fmt.Errorf("locking positions: %w", err)
	}

	return nil
}

// customerFloorCents is BR01: customer balances never go below zero.
const customerFloorCents = 0

const createCustomerSQL = `
WITH account AS (
    INSERT INTO accounts (id, kind, holder_name)
    VALUES ($1, 'customer', $2)
    RETURNING id, created_at
), balance AS (
    INSERT INTO account_balances (account_id, min_balance_cents)
    SELECT id, $3 FROM account
)
SELECT created_at FROM account`

// CreateCustomer inserts the account and its zero balance in one statement.
func (s LedgerStore) CreateCustomer(ctx context.Context, account ledger.Account) (time.Time, error) {
	var createdAt time.Time

	err := s.db.QueryRow(ctx, createCustomerSQL, uuid.UUID(account.ID), account.HolderName, customerFloorCents).Scan(&createdAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("creating customer account: %w", err)
	}

	return createdAt, nil
}

const findCustomerSQL = `
SELECT a.holder_name, a.created_at, b.balance_cents
FROM accounts a
JOIN account_balances b ON b.account_id = a.id
WHERE a.id = $1 AND a.kind = 'customer'`

// FindCustomer reads a customer account and its materialized balance.
func (s LedgerStore) FindCustomer(ctx context.Context, id ledger.AccountID) (ledger.Account, money.Balance, error) {
	account := ledger.Account{ID: id, Kind: ledger.AccountKindCustomer}

	var balanceCents int64

	err := s.db.QueryRow(ctx, findCustomerSQL, uuid.UUID(id)).Scan(&account.HolderName, &account.CreatedAt, &balanceCents)
	if err != nil {
		return ledger.Account{}, money.Balance{}, notFoundOr(err, "reading customer account")
	}

	return account, money.NewBalance(money.Cents(balanceCents)), nil
}

const readCustomerPositionSQL = `
SELECT b.balance_cents, b.last_entry_seq
FROM account_balances b
JOIN accounts a ON a.id = b.account_id
WHERE b.account_id = $1 AND a.kind = 'customer'`

// ReadCustomerPosition reads a customer's balance and last entry position,
// without locking the row.
func (s LedgerStore) ReadCustomerPosition(ctx context.Context, id ledger.AccountID) (ledger.Position, error) {
	row := s.db.QueryRow(ctx, readCustomerPositionSQL, uuid.UUID(id))

	position, err := scanPosition(row)
	if err != nil {
		return ledger.Position{}, notFoundOr(err, "reading customer position")
	}

	return position, nil
}

const overwritePositionSQL = `
UPDATE account_balances
SET balance_cents = $2, last_entry_seq = $3, updated_at = now()
WHERE account_id = $1`

// OverwritePosition stores a position the caller computed.
func (s LedgerStore) OverwritePosition(ctx context.Context, id ledger.AccountID, position ledger.Position) error {
	tag, err := s.db.Exec(ctx, overwritePositionSQL, uuid.UUID(id), int64(position.Balance.Cents()), int64(position.LastSeq))
	if err != nil {
		return fmt.Errorf("overwriting position: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return ledger.ErrAccountNotFound
	}

	return nil
}

const incrementBalanceSQL = `
UPDATE account_balances
SET balance_cents = balance_cents + $2, last_entry_seq = last_entry_seq + 1, updated_at = now()
WHERE account_id = $1
RETURNING balance_cents, last_entry_seq`

// IncrementBalance adds delta in the database and returns the new position.
func (s LedgerStore) IncrementBalance(ctx context.Context, id ledger.AccountID, delta money.Cents) (ledger.Position, error) {
	row := s.db.QueryRow(ctx, incrementBalanceSQL, uuid.UUID(id), int64(delta))

	position, err := scanPosition(row)
	if err != nil {
		return ledger.Position{}, notFoundOr(err, "incrementing balance")
	}

	return position, nil
}

const insertTransactionSQL = `
INSERT INTO ledger_transactions (id, kind, operation_id, client_ref, correlation_id, external_transfer_id, incoming_transfer_id, reconciliation_item_id)
VALUES ($1, $2, $3, nullif($4, ''), $5, $6, $7, $8)
RETURNING created_at`

// InsertTransaction appends a ledger transaction header.
func (s LedgerStore) InsertTransaction(ctx context.Context, transaction ledger.Transaction) (time.Time, error) {
	var createdAt time.Time

	var operationID *uuid.UUID
	if transaction.OperationID != nil {
		id := uuid.UUID(*transaction.OperationID)
		operationID = &id
	}

	row := s.db.QueryRow(ctx, insertTransactionSQL, uuid.UUID(transaction.ID), string(transaction.Kind), operationID, transaction.ClientRef, transaction.CorrelationID, transaction.ExternalTransferID, transaction.IncomingTransferID, transaction.ReconciliationItemID)

	err := row.Scan(&createdAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("inserting ledger transaction: %w", err)
	}

	return createdAt, nil
}

const insertEntrySQL = `
INSERT INTO ledger_entries (transaction_id, account_id, amount_cents, account_seq, balance_after_cents)
VALUES ($1, $2, $3, $4, $5)`

// InsertEntry appends one ledger entry.
func (s LedgerStore) InsertEntry(ctx context.Context, entry ledger.Entry) error {
	_, err := s.db.Exec(ctx, insertEntrySQL, uuid.UUID(entry.TransactionID), uuid.UUID(entry.AccountID), int64(entry.Amount), int64(entry.Seq), int64(entry.BalanceAfter.Cents()))
	if err != nil {
		return fmt.Errorf("inserting ledger entry: %w", err)
	}

	return nil
}

// The id breaks ties: without defense D2, two entries can share a position.
const readStatementSQL = `
SELECT e.account_seq, e.transaction_id, t.kind, e.amount_cents, e.balance_after_cents, e.created_at
FROM ledger_entries e
JOIN ledger_transactions t ON t.id = e.transaction_id
WHERE e.account_id = $1 AND e.account_seq > $2
ORDER BY e.account_seq, e.id
LIMIT $3`

// ReadStatement reads one page of an account's entries.
func (s LedgerStore) ReadStatement(ctx context.Context, query application.StatementQuery) ([]ledger.StatementLine, error) {
	rows, err := s.db.Query(ctx, readStatementSQL, uuid.UUID(query.AccountID), int64(query.AfterSeq), query.Limit)
	if err != nil {
		return nil, fmt.Errorf("reading statement: %w", err)
	}

	lines, err := pgx.CollectRows(rows, scanStatementLine)
	if err != nil {
		return nil, fmt.Errorf("reading statement: %w", err)
	}

	return lines, nil
}

func scanStatementLine(row pgx.CollectableRow) (ledger.StatementLine, error) {
	var (
		seq               int64
		transactionID     uuid.UUID
		kind              string
		amountCents       int64
		balanceAfterCents int64
		createdAt         time.Time
	)

	err := row.Scan(&seq, &transactionID, &kind, &amountCents, &balanceAfterCents, &createdAt)
	if err != nil {
		return ledger.StatementLine{}, fmt.Errorf("scanning a statement line: %w", err)
	}

	line := ledger.StatementLine{
		Seq:             ledger.Seq(seq),
		TransactionID:   ledger.TransactionID(transactionID),
		TransactionKind: ledger.TransactionKind(kind),
		Amount:          money.Cents(amountCents),
		BalanceAfter:    money.NewBalance(money.Cents(balanceAfterCents)),
		CreatedAt:       createdAt,
	}

	return line, nil
}

func scanPosition(row pgx.Row) (ledger.Position, error) {
	var balanceCents, lastSeq int64

	err := row.Scan(&balanceCents, &lastSeq)
	if err != nil {
		return ledger.Position{}, err
	}

	return ledger.Position{Balance: money.NewBalance(money.Cents(balanceCents)), LastSeq: ledger.Seq(lastSeq)}, nil
}

// notFoundOr turns a missing row into the domain error and wraps anything else.
func notFoundOr(err error, action string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.ErrAccountNotFound
	}

	return fmt.Errorf("%s: %w", action, err)
}
