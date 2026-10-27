package checker

import (
	"fmt"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

// mutations are the export mutations; the CI requires every one of them to be
// caught by the rule in want.
var mutations = []mutation{
	{name: "delete an entry", want: RuleI1aZeroSum, apply: deleteEntry},
	{name: "duplicate a transaction cleanly", want: RuleI2Duplicated, apply: duplicateTransaction},
	{name: "alter an amount", want: RuleI1aZeroSum, apply: alterAmount},
	{name: "alter a balance_after", want: RuleI1bChain, apply: alterBalanceAfter},
	{name: "alter a materialized balance", want: RuleI1bMaterialized, apply: alterMaterialized},
	{name: "remove an applied transaction", want: RuleI2Missing, apply: removeTransaction},
	{name: "add a phantom transaction", want: RuleI2Phantom, apply: addPhantom},
	{name: "point a transaction at another operation", want: RuleI2Missing, apply: retargetClientRef},
	{name: "change a transaction kind", want: RuleI2Missing, apply: changeKind},
	{name: "remove a created account", want: RuleI2MissingAccount, apply: removeAccount},
	{name: "store a negative balance_after", want: RuleI4EntryBalance, apply: negativeBalanceAfter},
	{name: "move an entry to another account", want: RuleI2Mismatch, apply: moveEntry},
}

// apply reports false when the round has nothing to mutate.
type mutation struct {
	name  string
	want  Rule
	apply func(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool)
}

func TestModel_CorrectRoundsAlwaysPass(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		result := generateRound(t).evaluate()

		if result.Verdict != VerdictPass {
			t.Fatalf("a correct round failed: %+v", result.Violations)
		}
	})
}

func TestMutations_AreAllDetectedByTheExpectedRule(t *testing.T) {
	t.Parallel()

	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			t.Parallel()

			rapid.Check(t, func(t *rapid.T) {
				b := generateRound(t)

				records, mutated, isApplied := m.apply(t, b)
				if !isApplied {
					t.Skip("nothing to mutate in this round")
				}

				rules := rulesOf(Evaluate(records, mutated))
				if !slices.Contains(rules, m.want) {
					t.Fatalf("mutation %q went undetected by %s (broken rules: %v)", m.name, m.want, rules)
				}
			})
		})
	}
}

// generateRound draws a correct round from a model of the bank: deposits,
// transfers that succeed only with funds, fails that touch nothing, reads
// that return the real balance and info deposits applied zero or one time.
func generateRound(t *rapid.T) *roundBuilder {
	b := newRoundBuilder(t)

	accounts := make([]string, rapid.IntRange(2, 5).Draw(t, "accounts"))
	for i := range accounts {
		accounts[i] = b.createAccount()
		b.deposit(accounts[i], rapid.Int64Range(1, 50_000).Draw(t, "funding"))
	}

	steps := rapid.IntRange(1, 40).Draw(t, "steps")

	for range steps {
		from := rapid.SampledFrom(accounts).Draw(t, "from")
		amount := rapid.Int64Range(1, 30_000).Draw(t, "amount")

		switch rapid.IntRange(0, 3).Draw(t, "kind") {
		case 0:
			b.deposit(from, amount)
		case 1:
			generateTransfer(t, b, accounts, from, amount)
		case 2:
			balance := b.positions[from].balance
			b.operation(history.FunctionReadBalance, readBalanceArgs{AccountID: from}, history.TypeOK, readBalanceValue{BalanceCents: balance})
		case 3:
			op := b.operation(history.FunctionDeposit, depositArgs{AccountID: from, AmountCents: amount}, history.TypeInfo, nil)

			isApplied := rapid.Bool().Draw(t, "info applied")
			if isApplied {
				b.apply(op, snapshot.TransactionDeposit, map[string]int64{from: amount, externalCash: -amount})
				b.resolve(op, outcomeApplied)

				continue
			}

			b.resolve(op, outcomeVoided)
		}
	}

	return b
}

func generateTransfer(t *rapid.T, b *roundBuilder, accounts []string, from string, amount int64) {
	others := slices.DeleteFunc(slices.Clone(accounts), func(a string) bool { return a == from })
	to := rapid.SampledFrom(others).Draw(t, "to")

	hasFunds := b.positions[from].balance >= amount
	if hasFunds {
		b.transfer(from, to, amount)

		return
	}

	b.operation(history.FunctionTransfer, transferArgs{From: from, To: to, AmountCents: amount}, history.TypeFail, nil)
}

func okTransactions(b *roundBuilder) []snapshot.Transaction {
	ops := indexOperations(b.records)

	var applied []snapshot.Transaction

	for _, transaction := range b.snap.Transactions {
		o, isKnown := ops.byID[transaction.ClientRef]
		isSucceeded := isKnown && o.result == history.TypeOK
		if isSucceeded {
			applied = append(applied, transaction)
		}
	}

	return applied
}

func pickTransaction(t *rapid.T, b *roundBuilder, kind snapshot.TransactionKind) (snapshot.Transaction, bool) {
	var candidates []snapshot.Transaction

	for _, transaction := range okTransactions(b) {
		isOfKind := kind == "" || transaction.Kind == kind
		if isOfKind {
			candidates = append(candidates, transaction)
		}
	}

	if len(candidates) == 0 {
		return snapshot.Transaction{}, false
	}

	return rapid.SampledFrom(candidates).Draw(t, "transaction"), true
}

func pickEntryIndex(t *rapid.T, state snapshot.Snapshot, isTarget func(snapshot.Entry) bool) (int, bool) {
	var candidates []int

	for i, entry := range state.Entries {
		if isTarget(entry) {
			candidates = append(candidates, i)
		}
	}

	if len(candidates) == 0 {
		return 0, false
	}

	return rapid.SampledFrom(candidates).Draw(t, "entry"), true
}

func isCustomerEntry(entry snapshot.Entry) bool {
	return entry.AccountID != externalCash
}

func deleteEntry(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	state := b.snapshot()

	i, isFound := pickEntryIndex(t, state, isCustomerEntry)
	if !isFound {
		return nil, state, false
	}

	state.Entries = slices.Delete(state.Entries, i, i+1)

	return b.records, state, true
}

// Entries in sequence and chained: only the link to the history can tell.
func duplicateTransaction(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	transaction, isFound := pickTransaction(t, b, "")
	if !isFound {
		return nil, snapshot.Snapshot{}, false
	}

	amounts := entryAmounts(entriesOf(b.snap, transaction.ID))
	b.apply(transaction.ClientRef, transaction.Kind, amounts)

	return b.records, b.snapshot(), true
}

func alterAmount(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	state := b.snapshot()
	i, _ := pickEntryIndex(t, state, func(snapshot.Entry) bool { return true })
	state.Entries[i].AmountCents++

	return b.records, state, true
}

func alterBalanceAfter(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	state := b.snapshot()
	i, _ := pickEntryIndex(t, state, func(snapshot.Entry) bool { return true })
	state.Entries[i].BalanceAfterCents += rapid.Int64Range(1, 1_000).Draw(t, "delta")

	return b.records, state, true
}

func alterMaterialized(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	state := b.snapshot()
	i := rapid.IntRange(0, len(state.Balances)-1).Draw(t, "balance")
	state.Balances[i].BalanceCents -= rapid.Int64Range(1, 1_000).Draw(t, "delta")

	return b.records, state, true
}

func removeTransaction(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	transaction, isFound := pickTransaction(t, b, "")
	if !isFound {
		return nil, snapshot.Snapshot{}, false
	}

	state := b.snapshot()
	state.Transactions = slices.DeleteFunc(state.Transactions, func(tx snapshot.Transaction) bool { return tx.ID == transaction.ID })
	state.Entries = slices.DeleteFunc(state.Entries, func(entry snapshot.Entry) bool { return entry.TransactionID == transaction.ID })

	return b.records, state, true
}

func addPhantom(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	customers := customerAccounts(b.snap)
	account := rapid.SampledFrom(customers).Draw(t, "account")
	phantomRef := fmt.Sprintf("r0001-p99-%06d", rapid.IntRange(1, 999_999).Draw(t, "phantom"))
	b.apply(phantomRef, snapshot.TransactionDeposit, map[string]int64{account: 100, externalCash: -100})

	return b.records, b.snapshot(), true
}

func retargetClientRef(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	transaction, isFound := pickTransaction(t, b, "")
	if !isFound {
		return nil, snapshot.Snapshot{}, false
	}

	var others []string

	for _, candidate := range okTransactions(b) {
		isOtherOfSameKind := candidate.Kind == transaction.Kind && candidate.ClientRef != transaction.ClientRef
		if isOtherOfSameKind {
			others = append(others, candidate.ClientRef)
		}
	}

	if len(others) == 0 {
		return nil, snapshot.Snapshot{}, false
	}

	state := b.snapshot()
	target := rapid.SampledFrom(others).Draw(t, "target")

	for i := range state.Transactions {
		if state.Transactions[i].ID == transaction.ID {
			state.Transactions[i].ClientRef = target
		}
	}

	return b.records, state, true
}

func changeKind(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	transaction, isFound := pickTransaction(t, b, snapshot.TransactionDeposit)
	if !isFound {
		return nil, snapshot.Snapshot{}, false
	}

	state := b.snapshot()

	for i := range state.Transactions {
		if state.Transactions[i].ID == transaction.ID {
			state.Transactions[i].Kind = snapshot.TransactionInternalTransfer
		}
	}

	return b.records, state, true
}

func removeAccount(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	state := b.snapshot()
	account := rapid.SampledFrom(customerAccounts(state)).Draw(t, "account")
	state.Accounts = slices.DeleteFunc(state.Accounts, func(a snapshot.Account) bool { return a.ID == account })

	return b.records, state, true
}

func negativeBalanceAfter(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	state := b.snapshot()
	i, _ := pickEntryIndex(t, state, isCustomerEntry)
	state.Entries[i].BalanceAfterCents = -rapid.Int64Range(1, 1_000).Draw(t, "negative")

	return b.records, state, true
}

func moveEntry(t *rapid.T, b *roundBuilder) ([]history.Record, snapshot.Snapshot, bool) {
	transaction, isFound := pickTransaction(t, b, "")
	if !isFound {
		return nil, snapshot.Snapshot{}, false
	}

	state := b.snapshot()

	i, _ := pickEntryIndex(t, state, func(entry snapshot.Entry) bool { return entry.TransactionID == transaction.ID })

	var destinations []string
	for _, account := range customerAccounts(state) {
		if account != state.Entries[i].AccountID {
			destinations = append(destinations, account)
		}
	}

	state.Entries[i].AccountID = rapid.SampledFrom(destinations).Draw(t, "destination")

	return b.records, state, true
}

func entriesOf(state snapshot.Snapshot, transactionID string) []snapshot.Entry {
	var entries []snapshot.Entry

	for _, entry := range state.Entries {
		if entry.TransactionID == transactionID {
			entries = append(entries, entry)
		}
	}

	return entries
}

func customerAccounts(state snapshot.Snapshot) []string {
	var accounts []string

	for _, account := range state.Accounts {
		if account.Kind == snapshot.KindCustomer {
			accounts = append(accounts, account.ID)
		}
	}

	return accounts
}
