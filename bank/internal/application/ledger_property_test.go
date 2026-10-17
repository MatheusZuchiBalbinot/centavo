package application_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"pgregory.net/rapid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

// memoryLedger is an in-memory bank with transactional semantics: a unit of
// work edits a copy and publishes it only when fn succeeds.
type memoryLedger struct {
	// The property moves money between customers only; PIX, the outbox, the
	// inbox, the leases and the reconciliation are covered by the
	// integration tests, so their stores are left unimplemented here.
	application.PixStore
	application.OutboxStore
	application.InboxStore
	application.LeaseStore
	application.ReconciliationStore

	positions    map[ledger.AccountID]ledger.Position
	isCustomer   map[ledger.AccountID]bool
	transactions []ledger.Transaction
	entries      []ledger.Entry
}

func newMemoryLedger() *memoryLedger {
	m := &memoryLedger{positions: map[ledger.AccountID]ledger.Position{}, isCustomer: map[ledger.AccountID]bool{}}
	m.positions[ledger.ExternalCashAccountID()] = ledger.Position{}

	return m
}

func (m *memoryLedger) clone() *memoryLedger {
	return &memoryLedger{
		positions:    maps.Clone(m.positions),
		isCustomer:   maps.Clone(m.isCustomer),
		transactions: slices.Clone(m.transactions),
		entries:      slices.Clone(m.entries),
	}
}

func (m *memoryLedger) Run(ctx context.Context, fn func(ctx context.Context, stores application.Stores) error) error {
	draft := m.clone()

	err := fn(ctx, draft)
	if err != nil {
		return err
	}

	*m = *draft

	return nil
}

func (m *memoryLedger) LockPositions(context.Context, []ledger.AccountID) error { return nil }

func (m *memoryLedger) ReadCustomerPosition(_ context.Context, id ledger.AccountID) (ledger.Position, error) {
	if !m.isCustomer[id] {
		return ledger.Position{}, ledger.ErrAccountNotFound
	}

	return m.positions[id], nil
}

func (m *memoryLedger) OverwritePosition(_ context.Context, id ledger.AccountID, position ledger.Position) error {
	m.positions[id] = position

	return nil
}

func (m *memoryLedger) IncrementBalance(_ context.Context, id ledger.AccountID, delta money.Cents) (ledger.Position, error) {
	current := m.positions[id]

	balance, err := current.Balance.Add(delta)
	if err != nil {
		return ledger.Position{}, err
	}

	next := ledger.Position{Balance: balance, LastSeq: current.LastSeq + 1}
	m.positions[id] = next

	return next, nil
}

func (m *memoryLedger) InsertTransaction(_ context.Context, transaction ledger.Transaction) (time.Time, error) {
	m.transactions = append(m.transactions, transaction)

	return time.Unix(0, 0), nil
}

func (m *memoryLedger) InsertEntry(_ context.Context, entry ledger.Entry) error {
	m.entries = append(m.entries, entry)

	return nil
}

// The property exercises money movement; keys and account creation are
// covered by the integration tests.
func (m *memoryLedger) ClaimKey(context.Context, application.KeyClaim) (idempotency.Record, bool, error) {
	return idempotency.Record{}, true, nil
}

func (m *memoryLedger) CompleteKey(context.Context, idempotency.Key, ledger.OperationID, idempotency.StoredResponse) error {
	return nil
}

func (m *memoryLedger) TouchKey(context.Context, idempotency.Key) error { return nil }

func (m *memoryLedger) VoidKey(context.Context, idempotency.Key) (idempotency.Record, bool, error) {
	return idempotency.Record{}, true, nil
}

func (m *memoryLedger) CreateCustomer(context.Context, ledger.Account) (time.Time, error) {
	return time.Unix(0, 0), nil
}

func (m *memoryLedger) openAccount() ledger.AccountID {
	id, err := ledger.NewAccountID()
	if err != nil {
		panic(err)
	}

	m.isCustomer[id] = true
	m.positions[id] = ledger.Position{}

	return id
}

func TestLedger_AnySequenceOfOperationsKeepsTheInvariants(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		bank := newMemoryLedger()
		deps := application.MoneyDependencies{UnitOfWork: bank, Locker: application.RowLocker{}}
		deposit, transfer := application.NewDeposit(deps), application.NewTransferInternal(deps)

		accounts := make([]ledger.AccountID, rapid.IntRange(2, 5).Draw(t, "accounts"))
		for i := range accounts {
			accounts[i] = bank.openAccount()
		}

		for step := range rapid.IntRange(1, 60).Draw(t, "steps") {
			amount, err := money.NewAmount(money.Cents(rapid.Int64Range(1, 50_000).Draw(t, "amount")))
			if err != nil {
				t.Fatal(err)
			}

			write := application.WriteContext{Key: idempotency.Key(fmt.Sprintf("prop-%06d", step))}
			from := rapid.SampledFrom(accounts).Draw(t, "from")

			if rapid.Bool().Draw(t, "is deposit") {
				_, err = deposit.Execute(context.Background(), application.DepositInput{AccountID: from, Amount: amount, Write: write})
			} else {
				to := rapid.SampledFrom(accounts).Draw(t, "to")
				_, err = transfer.Execute(context.Background(), application.TransferInput{From: from, To: to, Amount: amount, Write: write})
			}

			isExpectedRefusal := errors.Is(err, ledger.ErrInsufficientFunds) || errors.Is(err, ledger.ErrSameAccount)
			if err != nil && !isExpectedRefusal {
				t.Fatalf("step %d: %v", step, err)
			}
		}

		assertLedgerInvariants(t, bank)
	})
}

// assertLedgerInvariants is I1a, I1b and I4 over the in-memory ledger.
func assertLedgerInvariants(t *rapid.T, bank *memoryLedger) {
	sums := map[ledger.TransactionID]money.Cents{}
	counts := map[ledger.TransactionID]int{}
	running := map[ledger.AccountID]money.Cents{}
	lastSeq := map[ledger.AccountID]ledger.Seq{}

	for _, entry := range bank.entries {
		sums[entry.TransactionID] += entry.Amount
		counts[entry.TransactionID]++
		running[entry.AccountID] += entry.Amount

		isChained := entry.Seq == lastSeq[entry.AccountID]+1 && entry.BalanceAfter.Cents() == running[entry.AccountID]
		if !isChained {
			t.Fatalf("entry %+v breaks the chain of its account", entry)
		}

		lastSeq[entry.AccountID] = entry.Seq

		isOverdrawn := bank.isCustomer[entry.AccountID] && running[entry.AccountID] < 0
		if isOverdrawn {
			t.Fatalf("customer %s went to %d", entry.AccountID, running[entry.AccountID])
		}
	}

	for _, transaction := range bank.transactions {
		isBalanced := sums[transaction.ID] == 0 && counts[transaction.ID] >= 2
		if !isBalanced {
			t.Fatalf("transaction %s: %d entries summing %d", transaction.ID, counts[transaction.ID], sums[transaction.ID])
		}
	}

	for account, position := range bank.positions {
		isMaterialized := position.Balance.Cents() == running[account] && position.LastSeq == lastSeq[account]
		if !isMaterialized {
			t.Fatalf("account %s: materialized %+v, entries sum %d at %d", account, position, running[account], lastSeq[account])
		}
	}
}
