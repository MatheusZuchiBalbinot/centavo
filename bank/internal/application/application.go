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
	KeyStore
	CustomerCreator
	PixStore
	OutboxStore
	InboxStore
	LeaseStore
	ReconciliationStore
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

// newTransaction ties the ledger transaction to the operation; with D3 the
// operation id comes from the idempotency key, without it every request is
// a new operation and the key is only recorded as client_ref.
func newTransaction(kind ledger.TransactionKind, write WriteContext, operationID ledger.OperationID) (ledger.Transaction, error) {
	transactionID, err := ledger.NewTransactionID()
	if err != nil {
		return ledger.Transaction{}, err
	}

	transaction := ledger.Transaction{
		ID:            transactionID,
		Kind:          kind,
		OperationID:   &operationID,
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

// posting is one line of a ledger transaction: an account and how much it
// moves.
type posting struct {
	account ledger.AccountID
	delta   money.Cents
}

// debitLine is a customer debit: the account, the position ledger.Position.Debit
// computed for it and the amount taken.
type debitLine struct {
	account ledger.AccountID
	debited ledger.Position
	amount  money.Amount
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

// postAll posts a transaction's lines in account id order, whatever order
// the caller lists them in. Every write takes balances in that order, system
// accounts first, so two writers never wait on each other in a circle; a
// write that posted a customer before a system account deadlocked against a
// refund or a return several times a minute (see lock_order_test.go). It
// returns each account's new position.
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

// debit writes a customer's debit: the position the caller computed with
// ledger.Position.Debit, under the lock it took, and the matching entry. It
// is the one write of a balance computed here instead of incremented in SQL,
// so the funds check and the write see the same row (BR01, D2).
func (p poster) debit(ctx context.Context, transactionID ledger.TransactionID, line debitLine) error {
	err := p.positions.OverwritePosition(ctx, line.account, line.debited)
	if err != nil {
		return err
	}

	entry := ledger.Entry{
		TransactionID: transactionID,
		AccountID:     line.account,
		Amount:        line.amount.Negated(),
		Seq:           line.debited.LastSeq,
		BalanceAfter:  line.debited.Balance,
	}

	return p.entries.InsertEntry(ctx, entry)
}
