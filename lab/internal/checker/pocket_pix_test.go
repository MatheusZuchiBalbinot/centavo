package checker

import (
	"fmt"
	"testing"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

const (
	transit    = "sys-transit"
	settlement = "sys-settlement"
	suspense   = "sys-suspense"
)

// pixRound is a round builder with PIX out, a partner truth and the system
// accounts the PIX saga moves.
type pixRound struct {
	*roundBuilder
	truth     PartnerTruth
	nextTruth int64
	nextPix   int
}

func newPixRound(t *testing.T) *pixRound {
	t.Helper()

	b := newRoundBuilder(t)
	b.snap.Accounts = append(b.snap.Accounts,
		snapshot.Account{ID: transit, Kind: snapshot.KindSystem, SystemCode: snapshot.PixOutTransitCode},
		snapshot.Account{ID: settlement, Kind: snapshot.KindSystem, SystemCode: snapshot.PartnerSettlementCode},
		snapshot.Account{ID: suspense, Kind: snapshot.KindSystem, SystemCode: snapshot.SuspenseCode},
	)

	return &pixRound{roundBuilder: b, truth: PartnerTruth{ParticipantID: "centavo-robust"}}
}

// pixOut is a PIX out the client created and the bank reserved.
func (p *pixRound) pixOut(account string, cents int64) snapshot.ExternalTransfer {
	p.nextPix++
	id := fmt.Sprintf("00000000-0000-7000-8000-%012d", p.nextPix)
	op := p.upcomingOp()

	transfer := snapshot.ExternalTransfer{ID: id, EndToEndID: fixedEndToEnd(id), ClientRef: op, AccountID: account, AmountCents: cents, Status: statusCreated, StatusSeq: 1}
	p.snap.Transfers = append(p.snap.Transfers, transfer)

	p.operation(history.FunctionPixOut, pixOutArgs{AccountID: account, AmountCents: cents}, history.TypeOK, writeValue{TransferID: id})
	reserve := p.apply(op, snapshot.TransactionPixOutReserve, map[string]int64{account: -cents, transit: cents})
	p.linkTransfer(reserve, id)
	p.snap.Transitions = append(p.snap.Transitions, snapshot.Transition{TransferID: id, Seq: 1, To: statusCreated, LedgerTransactionID: reserve})

	return transfer
}

// step moves a transfer, posting what the target status implies.
func (p *pixRound) step(transfer snapshot.ExternalTransfer, from, to snapshot.TransferStatus) {
	var ledgerID string

	switch to {
	case statusConfirmed:
		ledgerID = p.apply("", snapshot.TransactionPixOutSettle, map[string]int64{transit: -transfer.AmountCents, settlement: transfer.AmountCents})
	case statusFailed:
		ledgerID = p.apply("", snapshot.TransactionPixOutRefund, map[string]int64{transit: -transfer.AmountCents, transfer.AccountID: transfer.AmountCents})
	case statusReversed:
		ledgerID = p.apply("", snapshot.TransactionPixOutReturn, map[string]int64{settlement: -transfer.AmountCents, transfer.AccountID: transfer.AmountCents})
	}

	if ledgerID != "" {
		p.linkTransfer(ledgerID, transfer.ID)
	}

	for i := range p.snap.Transfers {
		if p.snap.Transfers[i].ID != transfer.ID {
			continue
		}

		p.snap.Transfers[i].Status = to
		p.snap.Transfers[i].StatusSeq++
		seq := p.snap.Transfers[i].StatusSeq
		p.snap.Transitions = append(p.snap.Transitions, snapshot.Transition{TransferID: transfer.ID, Seq: seq, From: from, To: to, LedgerTransactionID: ledgerID})
	}
}

func (p *pixRound) linkTransfer(transactionID, transferID string) {
	for i := range p.snap.Transactions {
		if p.snap.Transactions[i].ID == transactionID {
			p.snap.Transactions[i].ExternalTransferID = transferID
		}
	}
}

// partnerSays records what the partner really did with the transfer.
func (p *pixRound) partnerSays(transfer snapshot.ExternalTransfer, status string, entries ...string) {
	p.truth.Transfers = append(p.truth.Transfers, truthTransfer{EndToEndID: transfer.EndToEndID, AmountCents: transfer.AmountCents, Status: status})

	for _, kind := range entries {
		p.nextTruth++
		p.truth.Statement = append(p.truth.Statement, truthEntry{Seq: p.nextTruth, Kind: kind, EndToEndID: transfer.EndToEndID, AmountCents: transfer.AmountCents})
	}
}

func (p *pixRound) evaluate() Result {
	truth := p.truth

	return EvaluateWithTruth(p.records, p.snapshot(), &truth)
}

func TestPocketPix_SettledTransferPasses(t *testing.T) {
	t.Parallel()

	p := newPixRound(t)
	alice := p.createAccount()
	p.deposit(alice, 10_000)

	settled := p.pixOut(alice, 4_500)
	p.step(settled, statusCreated, statusSent)
	p.step(settled, statusSent, statusConfirmed)
	p.partnerSays(settled, truthSettled, entrySettlement)

	refused := p.pixOut(alice, 1_000)
	p.step(refused, statusCreated, statusFailed)
	p.partnerSays(refused, truthRejected)

	assertPasses(t, p.evaluate())
}

// TestPocketPix_DuplicateWebhookSettlesTwice is FC24, PM-006.
func TestPocketPix_DuplicateWebhookSettlesTwice(t *testing.T) {
	t.Parallel()

	p := newPixRound(t)
	alice := p.createAccount()
	p.deposit(alice, 10_000)

	transfer := p.pixOut(alice, 4_500)
	p.step(transfer, statusCreated, statusConfirmed)
	p.step(transfer, statusConfirmed, statusConfirmed)
	p.partnerSays(transfer, truthSettled, entrySettlement)

	assertBreaks(t, p.evaluate(), RuleI7Path, RuleI7Postings, RuleI6Settlements, RuleI5Transit)
}

// TestPocketPix_LostPublishLeavesTheTransferStuck is FC04, PM-004.
func TestPocketPix_LostPublishLeavesTheTransferStuck(t *testing.T) {
	t.Parallel()

	p := newPixRound(t)
	alice := p.createAccount()
	p.deposit(alice, 10_000)
	p.pixOut(alice, 4_500)

	assertBreaks(t, p.evaluate(), RuleI5NotTerminal, RuleI5Transit)
}

// TestPocketPix_TimeoutTakenAsFailureCreatesMoney is FC21, PM-008: the
// partner paid, the bank refunded the customer too.
func TestPocketPix_TimeoutTakenAsFailureCreatesMoney(t *testing.T) {
	t.Parallel()

	p := newPixRound(t)
	alice := p.createAccount()
	p.deposit(alice, 10_000)

	transfer := p.pixOut(alice, 4_500)
	p.step(transfer, statusCreated, statusFailed)
	p.partnerSays(transfer, truthSettled, entrySettlement)

	assertBreaks(t, p.evaluate(), RuleI7Authority, RuleI6Settlements, RuleI1cPartnerReserve)
}

// TestPocketPix_RetryWithANewIDPaysTwice is FC05, PM-005: the naive relay
// sends E<id>-1 and E<id>-2, and the partner pays both.
func TestPocketPix_RetryWithANewIDPaysTwice(t *testing.T) {
	t.Parallel()

	p := newPixRound(t)
	alice := p.createAccount()
	p.deposit(alice, 10_000)

	transfer := p.pixOut(alice, 4_500)
	p.step(transfer, statusCreated, statusSent)
	p.step(transfer, statusSent, statusConfirmed)

	for _, attempt := range []string{"-1", "-2"} {
		retry := transfer
		retry.EndToEndID = transfer.EndToEndID + attempt
		p.partnerSays(retry, truthSettled, entrySettlement)
	}

	assertBreaks(t, p.evaluate(), RuleI6Settlements, RuleI1cPartnerReserve)
}

// TestPocketPix_PartnerDoubleProcessing is FC22, PM-010: the same
// end_to_end_id settled twice is money the partner owes.
func TestPocketPix_PartnerDoubleProcessing(t *testing.T) {
	t.Parallel()

	p := newPixRound(t)
	alice := p.createAccount()
	p.deposit(alice, 10_000)

	transfer := p.pixOut(alice, 4_500)
	p.step(transfer, statusCreated, statusConfirmed)
	p.partnerSays(transfer, truthSettled, entrySettlement, entrySettlement)

	assertBreaks(t, p.evaluate(), RuleI1cDoubleProcess, RuleI1cPartnerReserve)
}

// TestPocketPix_ReconciledDoubleProcessingPasses: the reconciliation booked
// the second settlement against suspense (D10), so the reserve mirror and
// what the partner owes both agree with the truth.
func TestPocketPix_ReconciledDoubleProcessingPasses(t *testing.T) {
	t.Parallel()

	p := newPixRound(t)
	alice := p.createAccount()
	p.deposit(alice, 10_000)

	transfer := p.pixOut(alice, 4_500)
	p.step(transfer, statusCreated, statusConfirmed)
	p.partnerSays(transfer, truthSettled, entrySettlement, entrySettlement)
	p.apply("", snapshot.TransactionReconciliationAdjustment, map[string]int64{settlement: 4_500, suspense: -4_500})

	assertPasses(t, p.evaluate())
}

func TestPocketPix_IncomingCreditedOrNot(t *testing.T) {
	t.Parallel()

	p := newPixRound(t)
	alice := p.createAccount()
	e2e := "E00000000000000000000000000000abc"

	p.operation(history.FunctionPixIn, pixInArgs{AccountID: alice, AmountCents: 900, EndToEndID: e2e}, history.TypeOK, map[string]bool{"accepted": true})
	p.truth.Incoming = append(p.truth.Incoming, truthIncoming{EndToEndID: e2e, AccountID: alice, AmountCents: 900})
	p.truth.Statement = append(p.truth.Statement, truthEntry{Seq: 1, Kind: entryIncoming, EndToEndID: e2e, AmountCents: 900})

	assertBreaks(t, p.evaluate(), RuleI2PixInCredit, RuleI6Incoming, RuleI1cPartnerReserve)

	credit := p.apply("", snapshot.TransactionPixInCredit, map[string]int64{alice: 900, settlement: -900})
	p.snap.Incoming = append(p.snap.Incoming, snapshot.Incoming{ID: "in-1", EndToEndID: e2e, AccountID: alice, AmountCents: 900})

	for i := range p.snap.Transactions {
		if p.snap.Transactions[i].ID == credit {
			p.snap.Transactions[i].IncomingTransferID = "in-1"
		}
	}

	assertPasses(t, p.evaluate())
}
