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

const lockPositionsSQL = `
SELECT account_id
FROM account_balances
WHERE account_id = ANY($1::uuid[])
ORDER BY account_id
FOR UPDATE`

// customerFloorCents is the lowest a customer balance may go.
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

const findCustomerSQL = `
SELECT a.holder_name, a.created_at, b.balance_cents
FROM accounts a
JOIN account_balances b ON b.account_id = a.id
WHERE a.id = $1 AND a.kind = 'customer'`

const readCustomerPositionSQL = `
SELECT b.balance_cents, b.last_entry_seq
FROM account_balances b
JOIN accounts a ON a.id = b.account_id
WHERE b.account_id = $1 AND a.kind = 'customer'`

const overwritePositionSQL = `
UPDATE account_balances
SET balance_cents = $2, last_entry_seq = $3, updated_at = now()
WHERE account_id = $1`

const incrementBalanceSQL = `
UPDATE account_balances
SET balance_cents = balance_cents + $2, last_entry_seq = last_entry_seq + 1, updated_at = now()
WHERE account_id = $1
RETURNING balance_cents, last_entry_seq`

const insertTransactionSQL = `
INSERT INTO ledger_transactions (id, kind, operation_id, client_ref, correlation_id, external_transfer_id, incoming_transfer_id, reconciliation_item_id)
VALUES ($1, $2, $3, nullif($4, ''), $5, $6, $7, $8)
RETURNING created_at`

const insertEntrySQL = `
INSERT INTO ledger_entries (transaction_id, account_id, amount_cents, account_seq, balance_after_cents)
VALUES ($1, $2, $3, $4, $5)`

// The id breaks ties: without the balance_locking defense, two entries can
// share a position.
const readStatementSQL = `
SELECT e.account_seq, e.transaction_id, t.kind, e.amount_cents, e.balance_after_cents, e.created_at
FROM ledger_entries e
JOIN ledger_transactions t ON t.id = e.transaction_id
WHERE e.account_id = $1 AND e.account_seq > $2
ORDER BY e.account_seq, e.id
LIMIT $3`

// LedgerStore implements the account, position and ledger interfaces of the
// application layer. Every method is a single statement; whether statements
// commit together depends on the querier it was built on.
type LedgerStore struct {
	ledgerRows
	keyStore
	pixStore
	outboxStore
	inboxStore
	leaseStore
	reconciliationStore
}

// querier is what both a pool and a transaction offer: on a pool every
// statement is its own autocommit, inside a transaction they commit together.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ledgerRows keeps balances, positions, transactions and entries.
type ledgerRows struct {
	db querier
}

// NewLedgerStore runs every statement in its own autocommit on the pool.
func NewLedgerStore(pool *pgxpool.Pool) LedgerStore {
	return storesOn(pool)
}

// storesOn builds every store over one querier: the pool, or the
// transaction of a unit of work.
func storesOn(db querier) LedgerStore {
	return LedgerStore{
		ledgerRows: ledgerRows{db: db}, keyStore: keyStore{db: db}, pixStore: pixStore{db: db},
		outboxStore: outboxStore{db: db}, inboxStore: inboxStore{db: db}, leaseStore: leaseStore{db: db}, reconciliationStore: reconciliationStore{db: db},
	}
}

// LockPositions takes the row locks of the accounts' balances in id order, so
// two transfers in opposite directions never wait for each other in a cycle.
// The locks last until the transaction ends.
func (s ledgerRows) LockPositions(ctx context.Context, ids []ledger.AccountID) error {
	uuids := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		uuids = append(uuids, uuid.UUID(id))
	}

	_, err := execute(ctx, s.db, newStatement("locking positions", lockPositionsSQL, uuids))

	return err
}

// CreateCustomer inserts the account and its zero balance in one statement.
func (s ledgerRows) CreateCustomer(ctx context.Context, account ledger.Account) (time.Time, error) {
	var createdAt time.Time

	create := newStatement("creating customer account", createCustomerSQL, uuid.UUID(account.ID), account.HolderName, customerFloorCents)
	err := scanRow(ctx, s.db, create, &createdAt)

	return createdAt, err
}

// FindCustomer reads a customer account and its materialized balance.
func (s ledgerRows) FindCustomer(ctx context.Context, id ledger.AccountID) (ledger.Account, money.Balance, error) {
	account := ledger.Account{ID: id, Kind: ledger.AccountKindCustomer}

	var balanceCents int64

	find := newStatement("reading customer account", findCustomerSQL, uuid.UUID(id))

	err := scanRow(ctx, s.db, find, &account.HolderName, &account.CreatedAt, &balanceCents)
	if err != nil {
		return ledger.Account{}, money.Balance{}, accountNotFoundOr(err)
	}

	return account, money.NewBalance(money.Cents(balanceCents)), nil
}

// ReadCustomerPosition reads a customer's balance and last entry position,
// without locking the row.
func (s ledgerRows) ReadCustomerPosition(ctx context.Context, id ledger.AccountID) (ledger.Position, error) {
	return s.readPosition(ctx, newStatement("reading customer position", readCustomerPositionSQL, uuid.UUID(id)))
}

// OverwritePosition stores a position the caller computed.
func (s ledgerRows) OverwritePosition(ctx context.Context, id ledger.AccountID, position ledger.Position) error {
	balanceCents := int64(position.Balance.Cents())
	overwrite := newStatement("overwriting position", overwritePositionSQL, uuid.UUID(id), balanceCents, int64(position.LastSeq))

	return executeOne(ctx, s.db, overwrite, ledger.ErrAccountNotFound)
}

// IncrementBalance adds delta in the database and returns the new position.
func (s ledgerRows) IncrementBalance(ctx context.Context, id ledger.AccountID, delta money.Cents) (ledger.Position, error) {
	return s.readPosition(ctx, newStatement("incrementing balance", incrementBalanceSQL, uuid.UUID(id), int64(delta)))
}

// InsertTransaction appends a ledger transaction header.
func (s ledgerRows) InsertTransaction(ctx context.Context, transaction ledger.Transaction) (time.Time, error) {
	var createdAt time.Time

	var operationID *uuid.UUID
	if transaction.OperationID != nil {
		id := uuid.UUID(*transaction.OperationID)
		operationID = &id
	}

	insert := newStatement(
		"inserting ledger transaction", insertTransactionSQL,
		uuid.UUID(transaction.ID), string(transaction.Kind), operationID, transaction.ClientRef, transaction.CorrelationID,
		transaction.ExternalTransferID, transaction.IncomingTransferID, transaction.ReconciliationItemID,
	)
	err := scanRow(ctx, s.db, insert, &createdAt)

	return createdAt, err
}

// InsertEntry appends one ledger entry.
func (s ledgerRows) InsertEntry(ctx context.Context, entry ledger.Entry) error {
	balanceAfterCents := int64(entry.BalanceAfter.Cents())
	insert := newStatement(
		"inserting ledger entry", insertEntrySQL,
		uuid.UUID(entry.TransactionID), uuid.UUID(entry.AccountID), int64(entry.Amount), int64(entry.Seq), balanceAfterCents,
	)

	_, err := execute(ctx, s.db, insert)

	return err
}

// ReadStatement reads one page of an account's entries.
func (s ledgerRows) ReadStatement(ctx context.Context, query application.StatementQuery) ([]ledger.StatementLine, error) {
	read := newStatement("reading statement", readStatementSQL, uuid.UUID(query.AccountID), int64(query.AfterSeq), query.Limit)

	return queryRows(ctx, s.db, read, scanStatementLine)
}

// readPosition reads the balance and last position a statement returns; a
// missing account is ledger.ErrAccountNotFound.
func (s ledgerRows) readPosition(ctx context.Context, st statement) (ledger.Position, error) {
	var balanceCents, lastSeq int64

	err := scanRow(ctx, s.db, st, &balanceCents, &lastSeq)
	if err != nil {
		return ledger.Position{}, accountNotFoundOr(err)
	}

	return ledger.Position{Balance: money.NewBalance(money.Cents(balanceCents)), LastSeq: ledger.Seq(lastSeq)}, nil
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
		Seq: ledger.Seq(seq), TransactionID: ledger.TransactionID(transactionID), TransactionKind: ledger.TransactionKind(kind), Amount: money.Cents(amountCents),
		BalanceAfter: money.NewBalance(money.Cents(balanceAfterCents)), CreatedAt: createdAt,
	}

	return line, nil
}

// accountNotFoundOr turns a missing row into the domain error.
func accountNotFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.ErrAccountNotFound
	}

	return err
}
