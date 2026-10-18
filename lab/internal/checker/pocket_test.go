package checker

import (
	"testing"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

// Pocket histories: one small round per bug pattern, written by hand.

func TestPocket_CorrectRoundPasses(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 10_000)
	b.transfer(alice, bob, 2_500)
	b.operation(history.FunctionReadBalance, readBalanceArgs{AccountID: alice}, history.TypeOK, readBalanceValue{BalanceCents: 7_500})
	b.operation(history.FunctionTransfer, transferArgs{From: bob, To: alice, AmountCents: 99_999}, history.TypeFail, nil)

	assertPasses(t, b.evaluate())
}

// TestPocket_LostUpdateOverdraws is a regression test: two debits of 60 read
// the same balance of 100, both pass the check and both write 40.
func TestPocket_LostUpdateOverdraws(t *testing.T) {
	t.Parallel()

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

// TestPocket_DebitWithoutCredit is a regression test: the process died between
// the two entries.
func TestPocket_DebitWithoutCredit(t *testing.T) {
	t.Parallel()

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

// TestPocket_RetryAppliedTwice is a regression test: the response was lost, the
// retry ran again.
func TestPocket_RetryAppliedTwice(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	op := b.deposit(alice, 1_000)
	b.apply(op, snapshot.TransactionDeposit, map[string]int64{alice: 1_000, externalCash: -1_000})

	result := b.evaluate()

	assertBreaks(t, result, RuleI2Duplicated, RuleI1cDeposits)
}

func TestPocket_IndeterminateAppliedTwice(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	op := b.operation(history.FunctionDeposit, depositArgs{AccountID: alice, AmountCents: 500}, history.TypeInfo, nil)
	b.apply(op, snapshot.TransactionDeposit, map[string]int64{alice: 500, externalCash: -500})
	b.resolve(op, outcomeApplied)

	assertPasses(t, b.evaluate())

	b.apply(op, snapshot.TransactionDeposit, map[string]int64{alice: 500, externalCash: -500})

	assertBreaks(t, b.evaluate(), RuleI3AtMostOnce)
}

func TestPocket_ResolveMustMatchTheLedger(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	voidedButApplied := b.operation(history.FunctionDeposit, depositArgs{AccountID: alice, AmountCents: 500}, history.TypeInfo, nil)
	b.apply(voidedButApplied, snapshot.TransactionDeposit, map[string]int64{alice: 500, externalCash: -500})
	b.resolve(voidedButApplied, outcomeVoided)

	assertBreaks(t, b.evaluate(), RuleI3Convergence)
}

// TestPocket_ReusedIdleKeyBelongsToItsSender is retry-storm's buggy client:
// it reused the key of a write that never applied, so its own deposit took
// the key. The deposit is the reuser's, and the first write never applied.
func TestPocket_ReusedIdleKeyBelongsToItsSender(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	owner := b.operation(history.FunctionDeposit, depositArgs{AccountID: alice, AmountCents: 500}, history.TypeInfo, nil)
	reuserArgs := map[string]any{"account_id": alice, "amount_cents": 300, "reused_key": owner}
	reuser := b.operation(history.FunctionDeposit, reuserArgs, history.TypeOK, writeValue{TransactionID: b.upcomingTransaction()})
	b.applyFor(owner, reuser, snapshot.TransactionDeposit, map[string]int64{alice: 300, externalCash: -300})
	b.resolve(owner, outcomeSuperseded)

	assertPasses(t, b.evaluate())
}

// TestPocket_IndeterminateReuseIsResolvedForItself: the buggy write that
// reused a key timed out too; resolve answers for its own request.
func TestPocket_IndeterminateReuseIsResolvedForItself(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	owner := b.deposit(alice, 500)
	reuserArgs := map[string]any{"account_id": alice, "amount_cents": 300, "reused_key": owner}
	reuser := b.operation(history.FunctionDeposit, reuserArgs, history.TypeInfo, nil)

	result := b.evaluate()
	if result.Verdict != VerdictInconclusive {
		t.Fatalf("an unresolved reuse: verdict %s", result.Verdict)
	}

	b.resolveWith(reuser, owner, outcomeSuperseded)

	assertPasses(t, b.evaluate())
}

// TestPocket_ReusedKeyWithTheSameRequestIsAReplay: the buggy client sent the
// same body under the reused key, so the bank answered with the stored
// response. The two operations are one deposit, counted once.
func TestPocket_ReusedKeyWithTheSameRequestIsAReplay(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	owner := b.deposit(alice, 500)
	stored := b.snap.Transactions[len(b.snap.Transactions)-1].ID
	replayArgs := map[string]any{"account_id": alice, "amount_cents": 500, "reused_key": owner}
	b.operation(history.FunctionDeposit, replayArgs, history.TypeOK, writeValue{TransactionID: stored})

	assertPasses(t, b.evaluate())
}

// TestPocket_ResolveOfASupersededKeyMustNotSayApplied is the bank answering
// the first write with the reuser's response, as it did before resolve
// compared the request.
func TestPocket_ResolveOfASupersededKeyMustNotSayApplied(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	owner := b.operation(history.FunctionDeposit, depositArgs{AccountID: alice, AmountCents: 500}, history.TypeInfo, nil)
	reuserArgs := map[string]any{"account_id": alice, "amount_cents": 300, "reused_key": owner}
	reuser := b.operation(history.FunctionDeposit, reuserArgs, history.TypeOK, writeValue{TransactionID: b.upcomingTransaction()})
	b.applyFor(owner, reuser, snapshot.TransactionDeposit, map[string]int64{alice: 300, externalCash: -300})
	b.resolve(owner, outcomeApplied)

	assertBreaks(t, b.evaluate(), RuleI3Convergence)
}

func TestPocket_ResolveMustNotChangeItsMind(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	op := b.operation(history.FunctionDeposit, depositArgs{AccountID: alice, AmountCents: 500}, history.TypeInfo, nil)
	b.resolve(op, outcomeVoided)
	b.resolve(op, outcomeApplied)

	assertBreaks(t, b.evaluate(), RuleI3Stability)
}

func TestPocket_UnresolvedMakesTheRoundInconclusive(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	b.operation(history.FunctionDeposit, depositArgs{AccountID: alice, AmountCents: 500}, history.TypeInfo, nil)

	result := b.evaluate()
	if result.Verdict != VerdictInconclusive || len(result.Unresolved) != 1 {
		t.Fatalf("verdict %s, unresolved %v", result.Verdict, result.Unresolved)
	}

	b.resolve(b.records[len(b.records)-1].Op, outcomeVoided)

	assertPasses(t, b.evaluate())
}

func TestPocket_ResolvedDepositsCloseTheDepositTermExactly(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice := b.createAccount()
	op := b.operation(history.FunctionDeposit, depositArgs{AccountID: alice, AmountCents: 500}, history.TypeInfo, nil)
	b.resolve(op, outcomeVoided)

	// Resolve voided the deposit, yet the ledger applied it: the deposit term
	// and convergence both catch it.
	s := b.snapshot()
	s.Transactions = append(s.Transactions, snapshot.Transaction{ID: "tx-x", Kind: snapshot.TransactionDeposit, ClientRef: op})
	s.Entries = append(s.Entries,
		snapshot.Entry{ID: 900, TransactionID: "tx-x", AccountID: alice, AmountCents: 500, AccountSeq: 1, BalanceAfterCents: 500},
		snapshot.Entry{ID: 901, TransactionID: "tx-x", AccountID: externalCash, AmountCents: -500, AccountSeq: 1, BalanceAfterCents: -500},
	)
	s.Balances = setBalance(s.Balances, alice, 500, 1)
	s.Balances = setBalance(s.Balances, externalCash, -500, 1)

	assertBreaks(t, Evaluate(b.records, s), RuleI1cDeposits, RuleI3Convergence)
}

func TestPocket_FailThatWasApplied(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 1_000)
	op := b.operation(history.FunctionTransfer, transferArgs{From: alice, To: bob, AmountCents: 100}, history.TypeFail, nil)
	b.apply(op, snapshot.TransactionInternalTransfer, map[string]int64{alice: -100, bob: 100})

	assertBreaks(t, b.evaluate(), RuleI2FailApplied)
}

func TestPocket_PhantomWrite(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 1_000)
	b.apply("r0001-p09-000001", snapshot.TransactionInternalTransfer, map[string]int64{alice: -100, bob: 100})

	assertBreaks(t, b.evaluate(), RuleI2Phantom)
}

func TestPocket_NegativeReadsAndPages(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
