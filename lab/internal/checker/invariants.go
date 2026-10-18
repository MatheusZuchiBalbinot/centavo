package checker

import (
	"cmp"
	"fmt"
	"slices"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

// Invariant names one invariant of docs/invariants-and-checker.md, section 6.
type Invariant string

// The invariants the checker judges.
const (
	InvariantI1 Invariant = "I1"
	InvariantI2 Invariant = "I2"
	InvariantI3 Invariant = "I3"
	InvariantI4 Invariant = "I4"
)

// Rule names the precise rule inside an invariant.
type Rule string

// The rules inside each invariant.
const (
	RuleI1aZeroSum          Rule = "I1a.zero_sum"
	RuleI1bSequence         Rule = "I1b.account_seq"
	RuleI1bChain            Rule = "I1b.balance_chain"
	RuleI1bMaterialized     Rule = "I1b.materialized_balance"
	RuleI1cDeposits         Rule = "I1c.term_d"
	RuleI2Missing           Rule = "I2.missing_application"
	RuleI2Duplicated        Rule = "I2.duplicated_application"
	RuleI2Mismatch          Rule = "I2.application_mismatch"
	RuleI2FailApplied       Rule = "I2.fail_applied"
	RuleI2Phantom           Rule = "I2.phantom"
	RuleI2MissingAccount    Rule = "I2.missing_account"
	RuleI3AtMostOnce        Rule = "I3.at_most_once"
	RuleI3Convergence       Rule = "I3.convergence"
	RuleI3Stability         Rule = "I3.stability"
	RuleI4EntryBalance      Rule = "I4.entry_balance"
	RuleI4RealBalance       Rule = "I4.real_balance"
	RuleI4Materialized      Rule = "I4.materialized_balance"
	RuleI4ReadBalance       Rule = "I4.read_balance"
	RuleI4StatementPage     Rule = "I4.statement_page"
	minEntriesInTransaction      = 2
)

// Violation is one broken rule, placed in time by the operation behind it.
type Violation struct {
	Invariant     Invariant `json:"invariant"`
	Rule          Rule      `json:"rule"`
	TimeNS        int64     `json:"t_ns"`
	Op            string    `json:"op,omitempty"`
	AccountID     string    `json:"account_id,omitempty"`
	TransactionID string    `json:"transaction_id,omitempty"`
	Message       string    `json:"message"`
}

type evaluation struct {
	ops        operations
	ledger     ledger
	violations []Violation
	// unresolved are indeterminate writes resolve never answered; they make
	// a round inconclusive, never failed (docs/invariants-and-checker.md, section 8).
	unresolved []string
}

func (e *evaluation) add(v Violation) {
	e.violations = append(e.violations, v)
}

// checkZeroSum is I1a: every transaction of the round has at least two entries
// that sum to zero.
func (e *evaluation) checkZeroSum() {
	for _, id := range e.ledger.transactionIDs {
		transaction := e.ledger.transactions[id]
		entries := e.ledger.entriesByTx[id]

		var sum int64
		for _, entry := range entries {
			sum += entry.AmountCents
		}

		isBalanced := len(entries) >= minEntriesInTransaction && sum == 0
		if isBalanced {
			continue
		}

		e.add(Violation{
			Invariant: InvariantI1, Rule: RuleI1aZeroSum, TimeNS: e.ops.timeOf(transaction.ClientRef), Op: transaction.ClientRef, TransactionID: id,
			Message: fmt.Sprintf("transação %s (%s) tem %d lançamentos que somam %d centavos", id, transaction.Kind, len(entries), sum),
		})
	}
}

// checkAccounts runs I1b on every exported account and the entry-level part
// of I4 on customer accounts.
func (e *evaluation) checkAccounts() {
	for _, accountID := range e.ledger.accountIDs {
		entries := e.ledger.entriesByAccount[accountID]

		e.checkSequence(accountID, entries)
		e.checkChain(accountID, entries)
		e.checkMaterialized(accountID, entries)

		if e.ledger.isCustomer(accountID) {
			e.checkNeverNegative(accountID, entries)
		}
	}
}

// bySeq orders entries by position; the id breaks ties between two entries a
// lost update put in the same position.
func bySeq(entries []snapshot.Entry) []snapshot.Entry {
	ordered := slices.Clone(entries)
	slices.SortFunc(ordered, func(a, b snapshot.Entry) int {
		return cmp.Or(cmp.Compare(a.AccountSeq, b.AccountSeq), cmp.Compare(a.ID, b.ID))
	})

	return ordered
}

func (e *evaluation) checkSequence(accountID string, entries []snapshot.Entry) {
	ordered := bySeq(entries)
	misplaced := 0
	first := -1

	for k, entry := range ordered {
		if entry.AccountSeq == int64(k+1) {
			continue
		}

		misplaced++
		if first < 0 {
			first = k
		}
	}

	if misplaced == 0 {
		return
	}

	entry := ordered[first]
	e.add(e.entryViolation(
		entry,
		RuleI1bSequence,
		fmt.Sprintf(
			"conta %s: o lançamento na posição %d tem account_seq %d; %d de %d lançamentos fora de 1..n (posição repetida ou buraco)",
			accountID,
			first+1,
			entry.AccountSeq,
			misplaced,
			len(ordered),
		),
	))
}

func (e *evaluation) checkChain(accountID string, entries []snapshot.Entry) {
	ordered := bySeq(entries)
	breaks := 0
	first := -1

	var previous int64

	for k, entry := range ordered {
		isChained := entry.BalanceAfterCents == previous+entry.AmountCents
		if !isChained {
			breaks++
			if first < 0 {
				first = k
			}
		}

		previous = entry.BalanceAfterCents
	}

	if breaks == 0 {
		return
	}

	entry := ordered[first]
	expected := entry.AmountCents
	if first > 0 {
		expected += ordered[first-1].BalanceAfterCents
	}

	e.add(e.entryViolation(
		entry,
		RuleI1bChain,
		fmt.Sprintf(
			"conta %s: na posição %d, balance_after é %d mas o anterior mais o valor dá %d; %d quebras no encadeamento",
			accountID,
			entry.AccountSeq,
			entry.BalanceAfterCents,
			expected,
			breaks,
		),
	))
}

func (e *evaluation) checkMaterialized(accountID string, entries []snapshot.Entry) {
	balance, hasBalance := e.ledger.balances[accountID]
	if !hasBalance {
		violation := Violation{
			Invariant: InvariantI1, Rule: RuleI1bMaterialized, TimeNS: unknownTime, AccountID: accountID,
			Message: fmt.Sprintf("conta %s não tem linha em account_balances", accountID),
		}

		e.add(violation)

		return
	}

	var sum int64
	for _, entry := range entries {
		sum += entry.AmountCents
	}

	isConsistent := balance.BalanceCents == sum && balance.LastEntrySeq == int64(len(entries))
	if isConsistent {
		return
	}

	e.add(Violation{
		Invariant: InvariantI1, Rule: RuleI1bMaterialized, TimeNS: e.lastEntryTime(entries), AccountID: accountID,
		Message: fmt.Sprintf(
			"conta %s: saldo materializado %d (última posição %d), mas os %d lançamentos somam %d",
			accountID, balance.BalanceCents, balance.LastEntrySeq, len(entries), sum,
		),
	})
}

// checkNeverNegative is I4 on the ledger: the stored balance_after, the real
// balance (the running sum of entries, in the order the bank wrote them) and
// the materialized balance never go below zero. The real balance is what
// "balance" means (docs/domain.md, section 1); a lost update can let it go
// negative while every stored number still looks fine.
func (e *evaluation) checkNeverNegative(accountID string, entries []snapshot.Entry) {
	var running int64

	isStoredReported, isRealReported := false, false

	for _, entry := range entries {
		running += entry.AmountCents

		isStoredNegative := entry.BalanceAfterCents < 0 && !isStoredReported
		if isStoredNegative {
			isStoredReported = true
			e.add(e.entryViolationOf(
				InvariantI4,
				entry,
				RuleI4EntryBalance,
				fmt.Sprintf("conta %s: lançamento na posição %d gravou balance_after %d", accountID, entry.AccountSeq, entry.BalanceAfterCents),
			))
		}

		isRealNegative := running < 0 && !isRealReported
		if isRealNegative {
			isRealReported = true
			e.add(e.entryViolationOf(
				InvariantI4,
				entry,
				RuleI4RealBalance,
				fmt.Sprintf(
					"conta %s: a soma real dos lançamentos chegou a %d centavos no lançamento %d (o banco aceitou um débito sem saldo)",
					accountID,
					running,
					entry.ID,
				),
			))
		}
	}

	balance, hasBalance := e.ledger.balances[accountID]

	isMaterializedNegative := hasBalance && balance.BalanceCents < 0
	if isMaterializedNegative {
		violation := Violation{
			Invariant: InvariantI4, Rule: RuleI4Materialized, TimeNS: e.lastEntryTime(entries), AccountID: accountID,
			Message: fmt.Sprintf("conta %s: saldo materializado negativo, %d", accountID, balance.BalanceCents),
		}

		e.add(violation)
	}
}

// checkDepositTerm is the D term of I1c, for this round: what left
// sys_external_cash must be what clients deposited, counting info deposits as
// "maybe" (until resolve exists, they only bound the term).
func (e *evaluation) checkDepositTerm() {
	if !e.ledger.hasExternalCashID {
		e.add(Violation{Invariant: InvariantI1, Rule: RuleI1cDeposits, TimeNS: unknownTime, Message: "a exportação não tem a conta sys_external_cash"})

		return
	}

	var confirmed, uncertain int64

	for _, o := range e.ops.ordered {
		if o.function != history.FunctionDeposit {
			continue
		}

		var args depositArgs
		decodeValidated(o.args, &args)

		switch e.depositStanding(o) {
		case standingApplied:
			confirmed += args.AmountCents
		case standingUnknown:
			uncertain += args.AmountCents
		case standingNotApplied:
		}
	}

	var inLedger int64

	for _, entry := range e.ledger.entriesByAccount[e.ledger.externalCashID] {
		transaction, isRoundTransaction := e.ledger.transactions[entry.TransactionID]
		if isRoundTransaction && transaction.Kind == snapshot.TransactionDeposit {
			inLedger -= entry.AmountCents
		}
	}

	isWithinBounds := inLedger >= confirmed && inLedger <= confirmed+uncertain
	if isWithinBounds {
		return
	}

	e.add(Violation{
		Invariant: InvariantI1, Rule: RuleI1cDeposits, TimeNS: e.ops.lastT, AccountID: e.ledger.externalCashID,
		Message: fmt.Sprintf(
			"termo D: sys_external_cash registra %d centavos depositados na rodada, o histórico admite entre %d (ok) e %d (ok mais info)",
			inLedger, confirmed, confirmed+uncertain,
		),
	})
}

// checkOperations is I2 and the at-most-once part of I3, operation by operation.
func (e *evaluation) checkOperations() {
	for _, o := range e.ops.ordered {
		switch o.function {
		case history.FunctionDeposit, history.FunctionTransfer, history.FunctionPixOut:
			e.checkWrite(o)
		case history.FunctionPixIn:
			e.checkPixIn(o)
		case history.FunctionCreateAccount:
			e.checkCreatedAccount(o)
		case history.FunctionReadBalance:
			e.checkReadBalance(o)
		case history.FunctionReadStatement:
			e.checkStatementPage(o)
		case history.FunctionResolve, history.FunctionReadPixOut:
			// Read by the I3 and PIX checks, which follow the writes they answer.
		case history.FunctionKill,
			history.FunctionArmFailpoint,
			history.FunctionArmPartner,
			history.FunctionFailpointFired,
			history.FunctionHealStart,
			history.FunctionFreeze,
			history.FunctionFailover,
			history.FunctionDatabase,
			history.FunctionNetwork,
			history.FunctionSkewClock,
			history.FunctionHealComplete:
			// The nemesis's own records; no invariant judges them.
		}
	}
}

func (e *evaluation) checkWrite(o *operation) {
	if e.isReplayOfTheKey(o) {
		return
	}

	applications := e.applications(o)

	switch {
	case o.result == history.TypeOK:
		e.checkOKWrite(o, applications)
	case o.result == history.TypeFail && len(applications) > 0:
		e.add(e.opViolation(o, InvariantI2, RuleI2FailApplied, fmt.Sprintf("%s terminou em fail, mas foi aplicada %d vez(es)", o.function, len(applications))))
	case o.result == history.TypeInfo || o.isOpen():
		e.checkIndeterminate(o, len(applications))
	}
}

// checkIndeterminate is I3: at most one application, and resolve must agree
// with the ledger and with itself.
func (e *evaluation) checkIndeterminate(o *operation, applied int) {
	if applied > 1 {
		e.add(e.opViolation(o, InvariantI3, RuleI3AtMostOnce, fmt.Sprintf("%s indeterminada foi aplicada %d vezes", o.function, applied)))

		return
	}

	answers := e.ops.resolutions[o.op]
	if len(answers) == 0 {
		e.unresolved = append(e.unresolved, o.op)

		return
	}

	isStable := !slices.ContainsFunc(answers, func(answer string) bool { return answer != answers[0] })
	if !isStable {
		e.add(e.opViolation(o, InvariantI3, RuleI3Stability, fmt.Sprintf("resolve deu respostas diferentes para a mesma chave: %v", answers)))

		return
	}

	isConvergent := (answers[0] == outcomeApplied) == (applied == 1)
	if !isConvergent {
		e.add(e.opViolation(o, InvariantI3, RuleI3Convergence, fmt.Sprintf("resolve respondeu %s, mas o ledger tem %d aplicação(ões)", answers[0], applied)))
	}
}

// standing is what the round knows about whether a write applied.
type standing int

const (
	standingNotApplied standing = iota
	standingApplied
	standingUnknown
)

// depositStanding settles an operation with its result and, for an info,
// with resolve's answer.
func (e *evaluation) depositStanding(o *operation) standing {
	// A replay is the key owner's deposit again; the owner counts it.
	if e.isReplayOfTheKey(o) {
		return standingNotApplied
	}

	if o.result == history.TypeOK {
		return standingApplied
	}

	isIndeterminate := o.result == history.TypeInfo || o.isOpen()
	if !isIndeterminate {
		return standingNotApplied
	}

	answer, isResolved := e.ops.resolutionOf(o.op)
	if !isResolved {
		return standingUnknown
	}

	if answer == outcomeApplied {
		return standingApplied
	}

	return standingNotApplied
}

func (e *evaluation) checkOKWrite(o *operation, applications []snapshot.Transaction) {
	if len(applications) == 0 {
		e.add(e.opViolation(
			o,
			InvariantI2,
			RuleI2Missing,
			fmt.Sprintf("%s terminou em ok, mas não há transação %s com este client_ref", o.function, transactionKindOf[o.function]),
		))

		return
	}

	if len(applications) > 1 {
		e.add(e.opViolation(o, InvariantI2, RuleI2Duplicated, fmt.Sprintf("%s terminou em ok e foi aplicada %d vezes", o.function, len(applications))))

		return
	}

	applied := applications[0]

	var returned writeValue
	decodeValidated(o.value, &returned)

	returnedID, appliedID := returned.TransactionID, applied.ID
	if o.function == history.FunctionPixOut {
		returnedID, appliedID = returned.TransferID, applied.ExternalTransferID
	}

	if returnedID != appliedID {
		e.add(e.opViolation(o, InvariantI2, RuleI2Mismatch, fmt.Sprintf("o cliente recebeu o id %s, mas a aplicação é %s", returnedID, appliedID)))

		return
	}

	expected := e.expectedEntries(o)
	entries := e.ledger.entriesByTx[applied.ID]
	actual := entryAmounts(entries)

	isAsRequested := len(entries) == len(expected) && amountsEqual(expected, actual)
	if !isAsRequested {
		e.add(e.opViolation(
			o,
			InvariantI2,
			RuleI2Mismatch,
			fmt.Sprintf("os lançamentos de %s não batem com o pedido: esperado %v, gravado %v", applied.ID, expected, actual),
		))
	}
}

// expectedEntries is what each account must receive for the operation's args.
func (e *evaluation) expectedEntries(o *operation) map[string]int64 {
	if o.function == history.FunctionDeposit {
		var args depositArgs
		decodeValidated(o.args, &args)

		return map[string]int64{args.AccountID: args.AmountCents, e.ledger.externalCashID: -args.AmountCents}
	}

	if o.function == history.FunctionPixOut {
		var args pixOutArgs
		decodeValidated(o.args, &args)

		return map[string]int64{args.AccountID: -args.AmountCents, e.ledger.systemID(snapshot.PixOutTransitCode): args.AmountCents}
	}

	var args transferArgs
	decodeValidated(o.args, &args)

	return map[string]int64{args.From: -args.AmountCents, args.To: args.AmountCents}
}

// checkPhantoms is the last rule of I2: every client transaction of the round
// belongs to an operation the history knows, of the matching kind.
func (e *evaluation) checkPhantoms() {
	for _, id := range e.ledger.transactionIDs {
		transaction := e.ledger.transactions[id]
		if !isClientKind(transaction.Kind) {
			continue
		}

		o, isKnown := e.ops.byID[e.ownerOf(transaction)]
		isAttributed := isKnown && transactionKindOf[o.function] == transaction.Kind
		if isAttributed {
			continue
		}

		e.add(Violation{
			Invariant: InvariantI2, Rule: RuleI2Phantom, TimeNS: e.ops.timeOf(transaction.ClientRef),
			Op: transaction.ClientRef, TransactionID: id,
			Message: fmt.Sprintf(
				"transação %s (%s) com client_ref %q não corresponde a nenhuma operação desse tipo no histórico",
				id, transaction.Kind, transaction.ClientRef,
			),
		})
	}
}

// checkCreatedAccount is the mild form of I2 for accounts: an account the
// client was told about exists.
func (e *evaluation) checkCreatedAccount(o *operation) {
	if o.result != history.TypeOK {
		return
	}

	var created createAccountValue
	decodeValidated(o.value, &created)

	if !e.ledger.isCustomer(created.AccountID) {
		e.add(e.opViolation(o, InvariantI2, RuleI2MissingAccount, fmt.Sprintf("conta %s devolvida ao cliente não existe no ledger", created.AccountID)))
	}
}

func (e *evaluation) checkReadBalance(o *operation) {
	if o.result != history.TypeOK {
		return
	}

	var read readBalanceValue
	decodeValidated(o.value, &read)

	if read.BalanceCents < 0 {
		var args readBalanceArgs
		decodeValidated(o.args, &args)

		violation := e.opViolation(o, InvariantI4, RuleI4ReadBalance, fmt.Sprintf("leitura de saldo devolveu %d", read.BalanceCents))
		violation.AccountID = args.AccountID
		e.add(violation)
	}
}

// checkStatementPage is I4 on reads: a page has contiguous positions, chained
// balances and no negative balance.
func (e *evaluation) checkStatementPage(o *operation) {
	if o.result != history.TypeOK {
		return
	}

	var page statementValue
	decodeValidated(o.value, &page)

	for i, entry := range page.Entries {
		if entry.BalanceAfterCents < 0 {
			e.add(e.opViolation(
				o,
				InvariantI4,
				RuleI4StatementPage,
				fmt.Sprintf("extrato mostra balance_after %d na posição %d", entry.BalanceAfterCents, entry.AccountSeq),
			))

			return
		}

		if i == 0 {
			continue
		}

		previous := page.Entries[i-1]

		isContiguous := entry.AccountSeq == previous.AccountSeq+1
		isChained := entry.BalanceAfterCents == previous.BalanceAfterCents+entry.AmountCents

		if !isContiguous || !isChained {
			e.add(e.opViolation(
				o,
				InvariantI4,
				RuleI4StatementPage,
				fmt.Sprintf(
					"extrato incoerente entre as posições %d e %d (balance_after %d e depois %d com valor %d)",
					previous.AccountSeq,
					entry.AccountSeq,
					previous.BalanceAfterCents,
					entry.BalanceAfterCents,
					entry.AmountCents,
				),
			))

			return
		}
	}
}

func (e *evaluation) opViolation(o *operation, invariant Invariant, rule Rule, message string) Violation {
	return Violation{Invariant: invariant, Rule: rule, TimeNS: o.invokeT, Op: o.op, Message: message}
}

func (e *evaluation) entryViolation(entry snapshot.Entry, rule Rule, message string) Violation {
	return e.entryViolationOf(InvariantI1, entry, rule, message)
}

func (e *evaluation) entryViolationOf(invariant Invariant, entry snapshot.Entry, rule Rule, message string) Violation {
	clientRef := e.ledger.transactions[entry.TransactionID].ClientRef

	return Violation{
		Invariant: invariant, Rule: rule, TimeNS: e.ops.timeOf(clientRef), Op: clientRef, AccountID: entry.AccountID,
		TransactionID: entry.TransactionID, Message: message,
	}
}

func (e *evaluation) lastEntryTime(entries []snapshot.Entry) int64 {
	if len(entries) == 0 {
		return unknownTime
	}

	last := entries[len(entries)-1]

	return e.ops.timeOf(e.ledger.transactions[last.TransactionID].ClientRef)
}

func entryAmounts(entries []snapshot.Entry) map[string]int64 {
	amounts := map[string]int64{}
	for _, entry := range entries {
		amounts[entry.AccountID] += entry.AmountCents
	}

	return amounts
}

// amountsEqual compares two account-to-amount maps.
func amountsEqual(expected, actual map[string]int64) bool {
	if len(expected) != len(actual) {
		return false
	}

	for account, amount := range expected {
		if actual[account] != amount {
			return false
		}
	}

	return true
}
