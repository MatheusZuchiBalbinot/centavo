// Package application runs the bank's use cases over storage interfaces.
//
// The money-moving use cases take two strategies that the defense profile
// picks (docs/architecture.md, section 8): a UnitOfWork, which is one
// transaction with D1 and plain autocommit without it, and a Locker, which
// takes the balance row locks in id order with D2 and nothing without it.
// With both off, the steps below are exactly the naive bank of phase F1:
// partial writes on a crash (FC01) and lost updates under concurrency (FC41).
// The idempotency key is still only recorded as client_ref until D3 (FC02).
package application

import (
	"bytes"
	"context"
	"slices"
	"time"

	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

// CustomerCreator persists a new customer account with an empty balance.
type CustomerCreator interface {
	CreateCustomer(ctx context.Context, account ledger.Account) (time.Time, error)
}

// CustomerReader reads a customer account and its materialized balance.
type CustomerReader interface {
	FindCustomer(ctx context.Context, id ledger.AccountID) (ledger.Account, money.Balance, error)
}

// PositionReader reads where a customer account stands, without locking it.
// It returns ledger.ErrAccountNotFound for unknown and system accounts.
type PositionReader interface {
	ReadCustomerPosition(ctx context.Context, id ledger.AccountID) (ledger.Position, error)
}

// PositionWriter moves materialized balances, each call in its own statement.
type PositionWriter interface {
	// OverwritePosition stores a position computed by the application.
	OverwritePosition(ctx context.Context, id ledger.AccountID, position ledger.Position) error
	// IncrementBalance adds delta in the database and returns the new position.
	IncrementBalance(ctx context.Context, id ledger.AccountID, delta money.Cents) (ledger.Position, error)
}

// PositionStore reads and moves customer positions.
type PositionStore interface {
	PositionReader
	PositionWriter
}

// LedgerWriter appends ledger transactions and entries.
type LedgerWriter interface {
	InsertTransaction(ctx context.Context, transaction ledger.Transaction) (time.Time, error)
	InsertEntry(ctx context.Context, entry ledger.Entry) error
}

// StatementReader reads one page of an account's entries in account_seq order.
type StatementReader interface {
	ReadStatement(ctx context.Context, query StatementQuery) ([]ledger.StatementLine, error)
}

// PositionLocker takes the row locks of balances for the rest of the unit of work.
type PositionLocker interface {
	LockPositions(ctx context.Context, ids []ledger.AccountID) error
}

// Stores are the repositories of one unit of work.
type Stores interface {
	PositionStore
	PositionLocker
	LedgerWriter
}

// UnitOfWork runs fn with stores whose writes commit together (D1) or one by
// one (naive). fn's error rolls a real transaction back.
type UnitOfWork interface {
	Run(ctx context.Context, fn func(ctx context.Context, stores Stores) error) error
}

// Locker decides which balance rows a use case locks before reading them.
type Locker interface {
	Lock(ctx context.Context, stores Stores, ids []ledger.AccountID) error
}

// RowLocker is defense D2: it locks every balance the use case will touch,
// in id order, before reading any of them. The id order is what keeps two
// transfers in opposite directions from deadlocking.
type RowLocker struct{}

// Lock sorts the ids and locks their rows.
func (RowLocker) Lock(ctx context.Context, stores Stores, ids []ledger.AccountID) error {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, compareAccountIDs)

	return stores.LockPositions(ctx, slices.Compact(ordered))
}

func compareAccountIDs(a, b ledger.AccountID) int {
	return bytes.Compare(a[:], b[:])
}

// MoneyDependencies are the strategies a money-moving use case runs on.
type MoneyDependencies struct {
	UnitOfWork UnitOfWork
	Locker     Locker
}

// WriteContext identifies the client request behind a write.
type WriteContext struct {
	Key           idempotency.Key
	CorrelationID string
}

func newTransaction(kind ledger.TransactionKind, write WriteContext) (ledger.Transaction, error) {
	transactionID, err := ledger.NewTransactionID()
	if err != nil {
		return ledger.Transaction{}, err
	}

	// The naive bank executes every request as a new operation: the key is
	// recorded, never checked.
	operationID, err := ledger.NewOperationID()
	if err != nil {
		return ledger.Transaction{}, err
	}

	transaction := ledger.Transaction{
		ID:            transactionID,
		Kind:          kind,
		OperationID:   operationID,
		ClientRef:     string(write.Key),
		CorrelationID: write.CorrelationID,
	}

	return transaction, nil
}

// poster moves one account's balance and appends the matching entry.
type poster struct {
	positions PositionWriter
	entries   LedgerWriter
}

// post adds delta to the account in the database and appends the entry. The
// increment is atomic, so concurrent posts never lose each other; only the
// debit path, which checks funds first, writes a value it computed itself.
func (p poster) post(ctx context.Context, transactionID ledger.TransactionID, accountID ledger.AccountID, delta money.Cents) (ledger.Position, error) {
	position, err := p.positions.IncrementBalance(ctx, accountID, delta)
	if err != nil {
		return ledger.Position{}, err
	}

	entry := ledger.Entry{TransactionID: transactionID, AccountID: accountID, Amount: delta, Seq: position.LastSeq, BalanceAfter: position.Balance}

	err = p.entries.InsertEntry(ctx, entry)
	if err != nil {
		return ledger.Position{}, err
	}

	return position, nil
}
