package checker

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

const externalCash = "sys-external-cash"

// fataler is what the builder needs from *testing.T and *rapid.T alike.
type fataler interface {
	Helper()
	Fatal(args ...any)
}

// roundBuilder writes a history and the ledger a correct bank would leave
// behind; tests then plant faults in either side.
type roundBuilder struct {
	t         fataler
	records   []history.Record
	snap      snapshot.Snapshot
	nextIndex int64
	nextEntry int64
	nextTx    int
	nextOp    int
	positions map[string]position
}

type position struct {
	balance int64
	seq     int64
}

func newRoundBuilder(t fataler) *roundBuilder {
	b := &roundBuilder{t: t, positions: map[string]position{}}
	b.snap.Meta.Round = "r0001"
	b.snap.Accounts = append(b.snap.Accounts, snapshot.Account{ID: externalCash, Kind: snapshot.KindSystem, SystemCode: snapshot.ExternalCashCode})

	return b
}

// record appends a history record; time advances one millisecond per record.
func (b *roundBuilder) record(record history.Record) {
	b.nextIndex++
	record.Index = b.nextIndex
	record.Round = b.snap.Meta.Round
	record.TimeNS = b.nextIndex * 1_000_000
	b.records = append(b.records, record)
}

func (b *roundBuilder) newOp() string {
	op := b.upcomingOp()
	b.nextOp++

	return op
}

// upcomingOp is the id the next operation will get.
func (b *roundBuilder) upcomingOp() string {
	return fmt.Sprintf("r0001-p01-%06d", b.nextOp+1)
}

// upcomingTransaction is the id the next applied transaction will get.
func (b *roundBuilder) upcomingTransaction() string {
	return fmt.Sprintf("tx-%04d", b.nextTx+1)
}

// operation records an invoke and its completion, returning the op id.
func (b *roundBuilder) operation(function history.Function, args any, result history.Type, value any) string {
	op := b.newOp()
	b.record(history.Record{Process: history.Client(1), Type: history.TypeInvoke, F: function, Op: op, Value: mustJSON(b.t, args)})

	completion := history.Record{Process: history.Client(1), Type: result, F: function, Op: op}
	if value != nil {
		completion.Value = mustJSON(b.t, value)
	}

	b.record(completion)

	return op
}

// apply writes a correct ledger transaction: entries in sequence, chained.
func (b *roundBuilder) apply(clientRef, kind string, amounts map[string]int64) {
	id := b.upcomingTransaction()
	b.nextTx++
	b.snap.Transactions = append(b.snap.Transactions, snapshot.Transaction{ID: id, Kind: kind, ClientRef: clientRef, CorrelationID: clientRef})

	for _, account := range slices.Sorted(maps.Keys(amounts)) {
		b.post(id, account, amounts[account])
	}
}

func (b *roundBuilder) post(transactionID, account string, amount int64) {
	current := b.positions[account]
	next := position{balance: current.balance + amount, seq: current.seq + 1}
	b.positions[account] = next

	b.nextEntry++
	entry := snapshot.Entry{ID: b.nextEntry, TransactionID: transactionID, AccountID: account, AmountCents: amount, AccountSeq: next.seq, BalanceAfterCents: next.balance}
	b.snap.Entries = append(b.snap.Entries, entry)
}

func (b *roundBuilder) createAccount() string {
	account := fmt.Sprintf("acc-%02d", len(b.snap.Accounts))
	b.operation(history.FunctionCreateAccount, map[string]string{"holder_name": account}, history.TypeOK, createAccountValue{AccountID: account})
	b.snap.Accounts = append(b.snap.Accounts, snapshot.Account{ID: account, Kind: snapshot.KindCustomer})

	return account
}

func (b *roundBuilder) deposit(account string, amount int64) string {
	op := b.upcomingOp()
	transactionID := b.upcomingTransaction()
	b.operation(history.FunctionDeposit, depositArgs{AccountID: account, AmountCents: amount}, history.TypeOK, writeValue{TransactionID: transactionID})
	b.apply(op, snapshot.TransactionDeposit, map[string]int64{account: amount, externalCash: -amount})

	return op
}

func (b *roundBuilder) transfer(from, to string, amount int64) {
	op := b.upcomingOp()
	transactionID := b.upcomingTransaction()
	b.operation(history.FunctionTransfer, transferArgs{From: from, To: to, AmountCents: amount}, history.TypeOK, writeValue{TransactionID: transactionID})
	b.apply(op, snapshot.TransactionInternalTransfer, map[string]int64{from: -amount, to: amount})
}

// snapshot returns the export, with materialized balances that match the entries.
func (b *roundBuilder) snapshot() snapshot.Snapshot {
	s := b.snap
	s.Balances = nil

	for _, account := range s.Accounts {
		current := b.positions[account.ID]
		s.Balances = append(s.Balances, snapshot.Balance{AccountID: account.ID, BalanceCents: current.balance, LastEntrySeq: current.seq})
	}

	s.Accounts = slices.Clone(s.Accounts)
	s.Transactions = slices.Clone(s.Transactions)
	s.Entries = slices.Clone(s.Entries)

	return s
}

func (b *roundBuilder) evaluate() Result {
	return Evaluate(b.records, b.snapshot())
}

func mustJSON(t fataler, value any) json.RawMessage {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return encoded
}

// rulesOf lists the distinct rules a result broke.
func rulesOf(result Result) []Rule {
	var rules []Rule

	for _, v := range result.Violations {
		if !slices.Contains(rules, v.Rule) {
			rules = append(rules, v.Rule)
		}
	}

	return rules
}

func assertPasses(t *testing.T, result Result) {
	t.Helper()

	if result.Verdict != VerdictPass {
		t.Fatalf("a correct round failed: %+v", result.Violations)
	}
}

func assertBreaks(t *testing.T, result Result, want ...Rule) {
	t.Helper()

	got := rulesOf(result)

	for _, rule := range want {
		if !slices.Contains(got, rule) {
			t.Fatalf("expected %s among the broken rules, got %v: %+v", rule, got, result.Violations)
		}
	}
}
