package checker

import (
	"encoding/json"
	"fmt"
	"strings"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

// Invariants of the PIX saga (docs/invariants-and-checker.md, section 6).
const (
	InvariantI5 Invariant = "I5"
	InvariantI6 Invariant = "I6"
	InvariantI7 Invariant = "I7"
)

// Rules of the PIX invariants.
const (
	RuleI1cPartnerReserve Rule = "I1c.term_r"
	RuleI1cDoubleProcess  Rule = "I1c.term_s"
	RuleI2PixInCredit     Rule = "I2.pix_in_credit" //nolint:gosec // G101: a rule name, not a credential.
	RuleI5NotTerminal     Rule = "I5.not_terminal"
	RuleI5Transit         Rule = "I5.transit"
	RuleI6Settlements     Rule = "I6.settlements"
	RuleI6Returns         Rule = "I6.returns"
	RuleI6Incoming        Rule = "I6.incoming"
	RuleI7Path            Rule = "I7.path"
	RuleI7Postings        Rule = "I7.postings"
	RuleI7Authority       Rule = "I7.authoritative_status"
)

// Statuses of the bank's state machine and of the partner's truth.
const (
	statusCreated   = "created"
	statusSent      = "sent"
	statusConfirmed = "confirmed"
	statusFailed    = "failed"
	statusReversed  = "reversed"

	truthSettled    = "settled"
	truthRejected   = "rejected"
	truthReturned   = "returned"
	truthProcessing = "processing"

	entrySettlement = "settlement"
	entryReturn     = "return"
	entryIncoming   = "incoming"
)

// allowedTransitions is the checker's own copy of the state machine, written
// apart from the bank's domain and from its database table (ADR-007).
var allowedTransitions = map[[2]string]bool{
	{statusCreated, statusSent}:       true,
	{statusCreated, statusConfirmed}:  true,
	{statusCreated, statusFailed}:     true,
	{statusSent, statusConfirmed}:     true,
	{statusSent, statusFailed}:        true,
	{statusConfirmed, statusReversed}: true,
}

// terminalStatuses end the saga (I5).
var terminalStatuses = map[string]bool{statusConfirmed: true, statusFailed: true, statusReversed: true}

// authoritativeOf is what the partner's truth must say for a final status.
var authoritativeOf = map[string]string{statusConfirmed: truthSettled, statusFailed: truthRejected, statusReversed: truthReturned}

// PartnerTruth is the partner's record (GET /internal/truth), read with the
// checker's own types.
type PartnerTruth struct {
	ParticipantID string          `json:"participant_id"`
	Transfers     []truthTransfer `json:"transfers"`
	Statement     []truthEntry    `json:"statement"`
	Incoming      []truthIncoming `json:"incoming"`
	Pending       int             `json:"pending_deliveries"`
}

type truthTransfer struct {
	EndToEndID        string `json:"end_to_end_id"`
	AmountCents       int64  `json:"amount_cents"`
	Status            string `json:"status"`
	IsDoubleProcessed bool   `json:"double_processed"`
}

type truthEntry struct {
	Seq         int64  `json:"seq"`
	Kind        string `json:"kind"`
	EndToEndID  string `json:"end_to_end_id"`
	AmountCents int64  `json:"amount_cents"`
}

type truthIncoming struct {
	EndToEndID  string `json:"end_to_end_id"`
	AccountID   string `json:"account_id"`
	AmountCents int64  `json:"amount_cents"`
}

// truthIndex is the round's part of the truth, keyed by the bank's
// transfer: the naive relay sends E<id>-<attempt>, so every attempt of a
// transfer maps back to it by its base E<id>.
type truthIndex struct {
	statusByE2E   map[string]string
	settlements   map[string][]truthEntry
	returns       map[string][]truthEntry
	incomingByE2E map[string]truthIncoming
	doubleCents   int64
	reserveCents  int64
}

// baseEndToEnd strips the naive attempt suffix.
func baseEndToEnd(endToEndID string) string {
	base, _, _ := strings.Cut(endToEndID, "-")

	return base
}

// fixedEndToEnd is a transfer's end_to_end_id as the bank derives it.
func fixedEndToEnd(transferID string) string {
	return "E" + strings.ReplaceAll(transferID, "-", "")
}

func indexTruth(truth *PartnerTruth, l ledger) truthIndex {
	index := truthIndex{statusByE2E: map[string]string{}, settlements: map[string][]truthEntry{}, returns: map[string][]truthEntry{}, incomingByE2E: map[string]truthIncoming{}}

	roundTransfers := map[string]bool{}
	for _, transfer := range l.transfers {
		roundTransfers[fixedEndToEnd(transfer.ID)] = true
	}

	for _, transfer := range truth.Transfers {
		index.statusByE2E[transfer.EndToEndID] = transfer.Status
	}

	for _, incoming := range truth.Incoming {
		if l.isCustomer(incoming.AccountID) {
			index.incomingByE2E[incoming.EndToEndID] = incoming
		}
	}

	for _, entry := range truth.Statement {
		index.addEntry(entry, roundTransfers)
	}

	return index
}

func (t *truthIndex) addEntry(entry truthEntry, roundTransfers map[string]bool) {
	base := baseEndToEnd(entry.EndToEndID)

	switch entry.Kind {
	case entrySettlement:
		if !roundTransfers[base] {
			return
		}

		// A second settlement of the same end_to_end_id is the partner's own
		// double processing: money it owes back (the S term).
		if t.settlementsOf(entry.EndToEndID, base) > 0 {
			t.doubleCents += entry.AmountCents
		}

		t.settlements[base] = append(t.settlements[base], entry)
		t.reserveCents -= entry.AmountCents
	case entryReturn:
		if roundTransfers[base] {
			t.returns[base] = append(t.returns[base], entry)
			t.reserveCents += entry.AmountCents
		}
	case entryIncoming:
		if _, isRound := t.incomingByE2E[entry.EndToEndID]; isRound {
			t.reserveCents += entry.AmountCents
		}
	}
}

// distinctPixOf counts the end_to_end_ids among a transfer's entries.
func distinctPixOf(entries []truthEntry) int {
	seen := map[string]bool{}

	for _, entry := range entries {
		seen[entry.EndToEndID] = true
	}

	return len(seen)
}

func (t *truthIndex) settlementsOf(endToEndID, base string) int {
	count := 0

	for _, entry := range t.settlements[base] {
		if entry.EndToEndID == endToEndID {
			count++
		}
	}

	return count
}

// checkPix runs I5, I6, I7 and the R and S terms of I1c. Without the
// partner's truth, only what the ledger alone can tell is checked.
func (e *evaluation) checkPix(truth *PartnerTruth) {
	for _, transfer := range e.ledger.transfers {
		e.checkPath(transfer)
		e.checkPostings(transfer)
		e.checkLiveness(transfer)
	}

	e.checkTransit()

	if truth == nil {
		return
	}

	index := indexTruth(truth, e.ledger)

	for _, transfer := range e.ledger.transfers {
		e.checkAgreement(transfer, index)
		e.checkAuthority(transfer, index)
	}

	e.checkIncoming(index)
	e.checkReserveTerms(index)
}

// checkPath is I7: the history starts at created, every step is allowed and
// follows the previous one, and the current status is the last step.
func (e *evaluation) checkPath(transfer snapshot.ExternalTransfer) {
	transitions := e.ledger.transitions[transfer.ID]
	previous := ""

	for k, transition := range transitions {
		isFirst := k == 0
		isValidFirst := isFirst && transition.From == "" && transition.To == statusCreated && transition.Seq == 1
		isValidStep := !isFirst && transition.From == previous && allowedTransitions[[2]string{transition.From, transition.To}] && transition.Seq == int64(k+1)

		if !isValidFirst && !isValidStep {
			e.add(e.transferViolation(transfer, InvariantI7, RuleI7Path, fmt.Sprintf("transferência %s: passo %d vai de %q para %q depois de %q", transfer.ID, transition.Seq, transition.From, transition.To, previous)))

			return
		}

		previous = transition.To
	}

	isCurrent := len(transitions) > 0 && transfer.Status == previous && transfer.StatusSeq == int64(len(transitions))
	if !isCurrent {
		e.add(e.transferViolation(transfer, InvariantI7, RuleI7Path, fmt.Sprintf("transferência %s está em %s (status_seq %d), mas o histórico tem %d passos e termina em %q", transfer.ID, transfer.Status, transfer.StatusSeq, len(transitions), previous)))
	}
}

// checkPostings is I7's ledger side: one reserve, and a settlement, refund
// or return exactly when the path goes through confirmed, failed or reversed.
func (e *evaluation) checkPostings(transfer snapshot.ExternalTransfer) {
	visited := map[string]bool{}
	for _, transition := range e.ledger.transitions[transfer.ID] {
		visited[transition.To] = true
	}

	counts := map[string]int{}
	for _, transaction := range e.ledger.byTransfer[transfer.ID] {
		counts[transaction.Kind]++
	}

	expected := map[string]int{
		snapshot.TransactionPixOutReserve: 1,
		snapshot.TransactionPixOutSettle:  boolCount(visited[statusConfirmed]),
		snapshot.TransactionPixOutRefund:  boolCount(visited[statusFailed]),
		snapshot.TransactionPixOutReturn:  boolCount(visited[statusReversed]),
	}

	for kind, want := range expected {
		if counts[kind] != want {
			e.add(e.transferViolation(transfer, InvariantI7, RuleI7Postings, fmt.Sprintf("transferência %s tem %d transação(ões) %s, o caminho exige %d", transfer.ID, counts[kind], kind, want)))
		}
	}
}

// checkLiveness is I5: at the end of the quiescence every transfer is final.
func (e *evaluation) checkLiveness(transfer snapshot.ExternalTransfer) {
	if terminalStatuses[transfer.Status] {
		return
	}

	e.add(e.transferViolation(transfer, InvariantI5, RuleI5NotTerminal, fmt.Sprintf("transferência %s ainda está em %s depois da quiescência", transfer.ID, transfer.Status)))
}

// checkTransit is I5 and BR13: once every transfer of the round is final,
// what the round put in sys_pix_out_transit came back out.
func (e *evaluation) checkTransit() {
	transitID := e.ledger.systemID(snapshot.PixOutTransitCode)

	var inTransit int64

	for _, entry := range e.ledger.entriesByAccount[transitID] {
		_, isRoundTransaction := e.ledger.transactions[entry.TransactionID]
		if isRoundTransaction {
			inTransit += entry.AmountCents
		}
	}

	if inTransit != 0 {
		e.add(Violation{Invariant: InvariantI5, Rule: RuleI5Transit, TimeNS: e.ops.lastT, AccountID: transitID, Message: fmt.Sprintf("sys_pix_out_transit guarda %d centavos da rodada depois da quiescência", inTransit)})
	}
}

// checkAgreement is I6 for one transfer: every PIX the partner settled or
// returned has exactly one ledger transaction, and back. A PIX is an
// end_to_end_id: the same one settled twice is the partner's double
// processing, which the S term accounts for, while a naive retry's new id is
// a second PIX the bank must book (or never have sent).
func (e *evaluation) checkAgreement(transfer snapshot.ExternalTransfer, index truthIndex) {
	base := fixedEndToEnd(transfer.ID)

	counts := map[string]int{}
	for _, transaction := range e.ledger.byTransfer[transfer.ID] {
		counts[transaction.Kind]++
	}

	settled, returned := distinctPixOf(index.settlements[base]), distinctPixOf(index.returns[base])

	if settled != counts[snapshot.TransactionPixOutSettle] {
		e.add(e.transferViolation(transfer, InvariantI6, RuleI6Settlements, fmt.Sprintf("o parceiro liquidou a transferência %s %d vez(es), o ledger registra %d", transfer.ID, settled, counts[snapshot.TransactionPixOutSettle])))
	}

	if returned != counts[snapshot.TransactionPixOutReturn] {
		e.add(e.transferViolation(transfer, InvariantI6, RuleI6Returns, fmt.Sprintf("o parceiro devolveu a transferência %s %d vez(es), o ledger registra %d", transfer.ID, returned, counts[snapshot.TransactionPixOutReturn])))
	}
}

// checkAuthority is I7's last rule: a final status agrees with the partner.
func (e *evaluation) checkAuthority(transfer snapshot.ExternalTransfer, index truthIndex) {
	want, isFinal := authoritativeOf[transfer.Status]
	if !isFinal {
		return
	}

	actual, isKnown := index.statusByE2E[transfer.EndToEndID]
	isNeverSent := !isKnown && transfer.Status == statusFailed
	if actual == want || isNeverSent {
		return
	}

	e.add(e.transferViolation(transfer, InvariantI7, RuleI7Authority, fmt.Sprintf("transferência %s terminou em %s, mas o parceiro diz %q para %s", transfer.ID, transfer.Status, cmpOrUnknown(actual), transfer.EndToEndID)))
}

// checkIncoming is I6 and BR12 for PIX in: each one the partner credited is
// credited exactly once in the ledger, and nothing else is.
func (e *evaluation) checkIncoming(index truthIndex) {
	for e2e := range index.incomingByE2E {
		credits := e.creditsOf(e2e)
		if credits != 1 {
			e.add(Violation{Invariant: InvariantI6, Rule: RuleI6Incoming, TimeNS: e.ops.lastT, Message: fmt.Sprintf("o parceiro creditou o PIX de entrada %s, o ledger tem %d crédito(s)", e2e, credits)})
		}
	}

	for e2e := range e.ledger.incomingByE2E {
		if _, isInTruth := index.incomingByE2E[e2e]; !isInTruth {
			e.add(Violation{Invariant: InvariantI6, Rule: RuleI6Incoming, TimeNS: e.ops.lastT, Message: fmt.Sprintf("o ledger creditou o PIX de entrada %s, que o parceiro não registra", e2e)})
		}
	}
}

func (e *evaluation) creditsOf(endToEndID string) int {
	credits := 0

	for _, incoming := range e.ledger.incomingByE2E[endToEndID] {
		credits += len(e.ledger.byIncoming[incoming.ID])
	}

	return credits
}

// checkPixIn is I2 for a PIX in: an ok request is credited exactly once.
func (e *evaluation) checkPixIn(o *operation) {
	if o.result != history.TypeOK {
		return
	}

	var args pixInArgs
	_ = json.Unmarshal(o.args, &args)

	credits := e.creditsOf(args.EndToEndID)
	if credits != 1 {
		e.add(e.opViolation(o, InvariantI2, RuleI2PixInCredit, fmt.Sprintf("PIX de entrada %s aceito pelo parceiro foi creditado %d vez(es)", args.EndToEndID, credits)))
	}
}

// checkReserveTerms is I1c's R and S terms for the round: the ledger's
// mirror of the partner reserve moved as much as the truth says, and the
// money the partner owes for double processing is what suspense holds.
func (e *evaluation) checkReserveTerms(index truthIndex) {
	settlementID := e.ledger.systemID(snapshot.PartnerSettlementCode)
	suspenseID := e.ledger.systemID(snapshot.SuspenseCode)

	// The truth's reserve counts double settlements too: the reconciliation
	// mirrors them with an adjustment against suspense, which the S term
	// checks below.
	mirrored := -e.roundSum(settlementID)
	if mirrored != index.reserveCents {
		e.add(Violation{Invariant: InvariantI1, Rule: RuleI1cPartnerReserve, TimeNS: e.ops.lastT, AccountID: settlementID, Message: fmt.Sprintf("termo R: sys_partner_settlement espelha %d centavos da rodada, a verdade do parceiro diz %d", mirrored, index.reserveCents)})
	}

	owed := -e.roundSum(suspenseID)
	if owed != index.doubleCents {
		e.add(Violation{Invariant: InvariantI1, Rule: RuleI1cDoubleProcess, TimeNS: e.ops.lastT, AccountID: suspenseID, Message: fmt.Sprintf("termo S: o parceiro processou %d centavos em dobro, a suspense guarda %d", index.doubleCents, owed)})
	}
}

// roundSum adds an account's entries that belong to the round's transactions.
func (e *evaluation) roundSum(accountID string) int64 {
	var sum int64

	for _, entry := range e.ledger.entriesByAccount[accountID] {
		_, isRoundTransaction := e.ledger.transactions[entry.TransactionID]
		if isRoundTransaction {
			sum += entry.AmountCents
		}
	}

	return sum
}

func (e *evaluation) transferViolation(transfer snapshot.ExternalTransfer, invariant Invariant, rule Rule, message string) Violation {
	return Violation{Invariant: invariant, Rule: rule, TimeNS: e.ops.timeOf(transfer.ClientRef), Op: transfer.ClientRef, AccountID: transfer.AccountID, Message: message}
}

func boolCount(value bool) int {
	if value {
		return 1
	}

	return 0
}

func cmpOrUnknown(status string) string {
	if status == "" {
		return "não registrado"
	}

	return status
}
