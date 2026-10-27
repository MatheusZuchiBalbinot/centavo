// Package application runs the bank's use cases over storage interfaces.
//
// The money-moving use cases take two strategies that the defense profile
// picks: a UnitOfWork, which is one transaction with atomic_ledger and plain
// autocommit without it, and a Locker, which takes the balance row locks in id
// order with balance_locking and nothing without it. With both off, the steps
// below are exactly the naive bank: a crash between two writes leaves a debit
// without its credit, and two concurrent writes to one balance lose an update.
// Without the idempotency defense, the key is only recorded as client_ref, so a
// retry applies the operation again.
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

type CustomerCreator interface {
	CreateCustomer(ctx context.Context, account ledger.Account) (time.Time, error)
}

type CustomerReader interface {
	FindCustomer(ctx context.Context, id ledger.AccountID) (ledger.Account, money.Balance, error)
}

// ledger.ErrAccountNotFound for unknown and system accounts.
type PositionReader interface {
	ReadCustomerPosition(ctx context.Context, id ledger.AccountID) (ledger.Position, error)
}

// Each call in its own statement.
type PositionWriter interface {
	OverwritePosition(ctx context.Context, id ledger.AccountID, position ledger.Position) error
	IncrementBalance(ctx context.Context, id ledger.AccountID, delta money.Cents) (ledger.Position, error)
}

type PositionStore interface {
	PositionReader
	PositionWriter
}

type LedgerWriter interface {
	InsertTransaction(ctx context.Context, transaction ledger.Transaction) (time.Time, error)
	InsertEntry(ctx context.Context, entry ledger.Entry) error
}

type StatementReader interface {
	ReadStatement(ctx context.Context, query StatementQuery) ([]ledger.StatementLine, error)
}

// The locks last for the rest of the unit of work.
type PositionLocker interface {
	LockPositions(ctx context.Context, ids []ledger.AccountID) error
}

type Stores interface {
	PositionStore
	PositionLocker
	LedgerWriter
	KeyStore
	CustomerCreator
	PixStore
	OutboxStore
	InboxStore
	LeaseStore
	ReconciliationStore
}

type PostingStores interface {
	PositionWriter
	LedgerWriter
}

type MoneyStores interface {
	PositionStore
	PositionLocker
	LedgerWriter
}

// Writes commit together with atomic_ledger, or one by one when naive. fn's
// error rolls a real transaction back.
type UnitOfWork interface {
	Run(ctx context.Context, fn func(ctx context.Context, stores Stores) error) error
}

type Locker interface {
	Lock(ctx context.Context, stores PositionLocker, ids []ledger.AccountID) error
}

// RowLocker is the balance_locking defense: it locks every balance the use case
// will touch, in id order, before reading any of them. The id order is what
// keeps two transfers in opposite directions from deadlocking.
type RowLocker struct{}

type MoneyDependencies struct {
	UnitOfWork UnitOfWork
	Locker     Locker
}

type WriteContext struct {
	Key           idempotency.Key
	CorrelationID string
}

type poster struct {
	positions PositionWriter
	entries   LedgerWriter
}

type posting struct {
	account ledger.AccountID
	delta   money.Cents
}

type debitLine struct {
	account ledger.AccountID
	debited ledger.Position
	amount  money.Amount
}

func (RowLocker) Lock(ctx context.Context, stores PositionLocker, ids []ledger.AccountID) error {
	ordered := slices.Clone(ids)
	slices.SortFunc(ordered, compareAccountIDs)

	return stores.LockPositions(ctx, slices.Compact(ordered))
}

func compareAccountIDs(a, b ledger.AccountID) int {
	return bytes.Compare(a[:], b[:])
}

// With idempotency the operation id comes from the key; without it every
// request is a new operation and the key is only recorded as client_ref.
func newTransaction(kind ledger.TransactionKind, write WriteContext, operationID ledger.OperationID) (ledger.Transaction, error) {
	transactionID, err := ledger.NewTransactionID()
	if err != nil {
		return ledger.Transaction{}, err
	}

	transaction := ledger.Transaction{
		ID: transactionID, Kind: kind, OperationID: &operationID, ClientRef: string(write.Key), CorrelationID: write.CorrelationID,
	}

	return transaction, nil
}

// The increment is atomic, so concurrent posts never lose each other; only
// the debit path, which checks funds first, writes a value it computed
// itself.
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

// In account id order, whatever order the caller lists them in: every write
// takes balances in that order, system accounts first, so two writers never
// wait on each other in a circle. A write that posted a customer before a
// system account deadlocked against a refund or a return several times a
// minute (see lock_order_test.go).
func (p poster) postAll(ctx context.Context, transactionID ledger.TransactionID, postings []posting) (map[ledger.AccountID]ledger.Position, error) {
	ordered := slices.Clone(postings)
	slices.SortFunc(ordered, func(a, b posting) int { return compareAccountIDs(a.account, b.account) })

	positions := make(map[ledger.AccountID]ledger.Position, len(ordered))

	for _, line := range ordered {
		position, err := p.post(ctx, transactionID, line.account, line.delta)
		if err != nil {
			return nil, err
		}

		positions[line.account] = position
	}

	return positions, nil
}

// The one balance write computed here instead of incremented in SQL, so the
// funds check and the write see the same locked row, and a customer never
// goes below zero.
func (p poster) debit(ctx context.Context, transactionID ledger.TransactionID, line debitLine) error {
	err := p.positions.OverwritePosition(ctx, line.account, line.debited)
	if err != nil {
		return err
	}

	entry := ledger.Entry{
		TransactionID: transactionID, AccountID: line.account, Amount: line.amount.Negated(), Seq: line.debited.LastSeq, BalanceAfter: line.debited.Balance,
	}

	return p.entries.InsertEntry(ctx, entry)
}
