package checker

import (
	"testing"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

// Pocket histories (docs/invariants-and-checker.md, section 12): one small
// round per bug pattern, written by hand.

func TestPocket_CorrectRoundPasses(t *testing.T) {
	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 10_000)
	b.transfer(alice, bob, 2_500)
	b.operation(history.FunctionReadBalance, readBalanceArgs{AccountID: alice}, history.TypeOK, readBalanceValue{BalanceCents: 7_500})
	b.operation(history.FunctionTransfer, transferArgs{From: bob, To: alice, AmountCents: 99_999}, history.TypeFail, nil)

	assertPasses(t, b.evaluate())
}

// TestPocket_LostUpdateOverdraws is PM-001: two debits of 60 read the same
// balance of 100, both pass the check and both write 40.
func TestPocket_LostUpdateOverdraws(t *testing.T) {
	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 100)

	first := b.upcomingOp()
	b.operation(history.FunctionTransfer, transferArgs{From: alice, To: bob, AmountCents: 60}, history.TypeOK, writeValue{TransactionID: "tx-lost-1"})
	second := b.upcomingOp()
	b.operation(history.FunctionTransfer, transferArgs{From: alice, To: bob, AmountCents: 60}, history.TypeOK, writeValue{TransactionID: "tx-lost-2"})

	s := b.snapshot()
	s.Transactions = append(s.Transactions,
		snapshot.Transaction{ID: "tx-lost-1", Kind: snapshot.TransactionInternalTransfer, ClientRef: first},
		snapshot.Transaction{ID: "tx-lost-2", Kind: snapshot.TransactionInternalTransfer, ClientRef: second},
	)
	s.Entries = append(s.Entries,
		snapshot.Entry{ID: 100, TransactionID: "tx-lost-1", AccountID: alice, AmountCents: -60, AccountSeq: 2, BalanceAfterCents: 40},
		snapshot.Entry{ID: 101, TransactionID: "tx-lost-2", AccountID: alice, AmountCents: -60, AccountSeq: 2, BalanceAfterCents: 40},
		snapshot.Entry{ID: 102, TransactionID: "tx-lost-1", AccountID: bob, AmountCents: 60, AccountSeq: 1, BalanceAfterCents: 60},
		snapshot.Entry{ID: 103, TransactionID: "tx-lost-2", AccountID: bob, AmountCents: 60, AccountSeq: 2, BalanceAfterCents: 120},
	)
	s.Balances = setBalance(s.Balances, alice, 40, 2)
	s.Balances = setBalance(s.Balances, bob, 120, 2)

	result := Evaluate(b.records, s)

	assertBreaks(t, result, RuleI1bSequence, RuleI1bChain, RuleI1bMaterialized, RuleI4RealBalance)
}

// TestPocket_DebitWithoutCredit is FC01: the process died between the two entries.
func TestPocket_DebitWithoutCredit(t *testing.T) {
	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 1_000)
	b.transfer(alice, bob, 300)

	s := b.snapshot()
	s.Entries = s.Entries[:len(s.Entries)-1]
	s.Balances = setBalance(s.Balances, bob, 0, 0)

	result := Evaluate(b.records, s)

	assertBreaks(t, result, RuleI1aZeroSum)
}

// TestPocket_RetryAppliedTwice is FC02: the response was lost, the retry ran again.
func TestPocket_RetryAppliedTwice(t *testing.T) {
	b := newRoundBuilder(t)
	alice := b.createAccount()
	op := b.deposit(alice, 1_000)
	b.apply(op, snapshot.TransactionDeposit, map[string]int64{alice: 1_000, externalCash: -1_000})

	result := b.evaluate()

	assertBreaks(t, result, RuleI2Duplicated, RuleI1cDeposits)
}

func TestPocket_IndeterminateAppliedTwice(t *testing.T) {
	b := newRoundBuilder(t)
	alice := b.createAccount()
	op := b.operation(history.FunctionDeposit, depositArgs{AccountID: alice, AmountCents: 500}, history.TypeInfo, nil)
	b.apply(op, snapshot.TransactionDeposit, map[string]int64{alice: 500, externalCash: -500})

	assertPasses(t, b.evaluate())

	b.apply(op, snapshot.TransactionDeposit, map[string]int64{alice: 500, externalCash: -500})

	assertBreaks(t, b.evaluate(), RuleI3AtMostOnce)
}

func TestPocket_FailThatWasApplied(t *testing.T) {
	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 1_000)
	op := b.operation(history.FunctionTransfer, transferArgs{From: alice, To: bob, AmountCents: 100}, history.TypeFail, nil)
	b.apply(op, snapshot.TransactionInternalTransfer, map[string]int64{alice: -100, bob: 100})

	assertBreaks(t, b.evaluate(), RuleI2FailApplied)
}

func TestPocket_PhantomWrite(t *testing.T) {
	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 1_000)
	b.apply("r0001-p09-000001", snapshot.TransactionInternalTransfer, map[string]int64{alice: -100, bob: 100})

	assertBreaks(t, b.evaluate(), RuleI2Phantom)
}

func TestPocket_NegativeReadsAndPages(t *testing.T) {
	b := newRoundBuilder(t)
	alice := b.createAccount()
	b.deposit(alice, 1_000)
	b.operation(history.FunctionReadBalance, readBalanceArgs{AccountID: alice}, history.TypeOK, readBalanceValue{BalanceCents: -1})

	page := statementValue{Entries: []statementEntry{
		{AccountSeq: 1, AmountCents: 1_000, BalanceAfterCents: 1_000},
		{AccountSeq: 1, AmountCents: -100, BalanceAfterCents: 900},
	}}
	b.operation(history.FunctionReadStatement, readBalanceArgs{AccountID: alice}, history.TypeOK, page)

	assertBreaks(t, b.evaluate(), RuleI4ReadBalance, RuleI4StatementPage)
}

func TestPocket_AccountTheClientWasToldAboutIsMissing(t *testing.T) {
	b := newRoundBuilder(t)
	b.operation(history.FunctionCreateAccount, map[string]string{"holder_name": "ghost"}, history.TypeOK, createAccountValue{AccountID: "acc-ghost"})

	assertBreaks(t, b.evaluate(), RuleI2MissingAccount)
}

func setBalance(balances []snapshot.Balance, account string, cents, seq int64) []snapshot.Balance {
	for i := range balances {
		if balances[i].AccountID == account {
			balances[i].BalanceCents = cents
			balances[i].LastEntrySeq = seq
		}
	}

	return balances
}
