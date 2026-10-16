// Package application runs the bank's use cases over storage interfaces.
//
// The money-moving use cases here are the naive variants of phase F1
// (docs/architecture.md, section 5.2): every write is its own autocommit
// statement, the debited balance is read and written back by the application,
// and the idempotency key is only recorded as client_ref. They are the code a
// reasonable developer writes in a hurry, and they carry three known bugs:
// partial writes on a crash (FC01), lost updates under concurrency (FC41) and
// re-execution on retry (FC02). The robust variants arrive with defenses D1 to
// D3 and keep these behind the naive profile.
package application

import (
	"context"
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

// MoneyDependencies are the stores a money-moving use case writes to.
type MoneyDependencies struct {
	Positions PositionStore
	Ledger    LedgerWriter
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
