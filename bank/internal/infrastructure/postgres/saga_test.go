package postgres_test

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
)

// sweptNow makes every transfer in created or sent count as stuck.
const sweptNow = time.Duration(0)

func (p pixBank) settlements(t *testing.T, id transfer.ID, kind string) int {
	t.Helper()

	return countRows(t, p, `SELECT count(*) FROM ledger_transactions WHERE external_transfer_id = $1 AND kind = $2`, uuid.UUID(id), kind)
}

func (p pixBank) sentTransfer(t *testing.T, cents int64) application.PixOutResult {
	t.Helper()

	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	created := p.createPixOut(t, account, cents)
	p.markSent(t, created.TransferID)

	return created
}

func receive(t *testing.T, intake application.InboxIntake, events ...application.PartnerEvent) {
	t.Helper()

	for _, event := range events {
		err := intake.Apply(t.Context(), event)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func newEventID() string {
	return "evt_" + uuid.NewString()
}

// TestTransfer_DuplicateEffectWithNewEventID_IsNoop is PM-007 (FC25): the
// partner sent "settled" twice under two event ids, which the inbox cannot
// tell apart. The second does not fit a confirmed transfer, so the
// processor asks the partner, finds it settled, and posts nothing more.
func TestTransfer_DuplicateEffectWithNewEventID_IsNoop(t *testing.T) {
	p := newOutboxBank(t)
	intake, processor := newInbox(p, statusAnswer(transfer.PartnerSettled))
	drainInbox(t, processor)

	created := p.sentTransfer(t, 4_000)
	first := application.PartnerEvent{ID: newEventID(), Type: application.EventSettled, EndToEndID: created.EndToEndID, AmountCents: 4_000}
	second := first
	second.ID = newEventID()

	receive(t, intake, first, second)
	drainInbox(t, processor)

	view := p.status(t, created.TransferID)
	isSettledOnce := view.Transfer.Status == transfer.StatusConfirmed && p.settlements(t, created.TransferID, "pix_out_settle") == 1
	if !isSettledOnce {
		t.Fatalf("status %s with %d settlements", view.Transfer.Status, p.settlements(t, created.TransferID, "pix_out_settle"))
	}

	if countRows(t, p, `SELECT count(*) FROM inbox_events WHERE event_id = $1 AND outcome = 'resynced'`, second.ID) != 1 {
		t.Fatal("the second settlement was not resynced")
	}
}

// TestStateMachine_ReturnBeforeSettlement_WalksThePath: "returned" arrived
// before "settled". The processor walks sent, confirmed, reversed, each
// step with its own posting.
func TestStateMachine_ReturnBeforeSettlement_WalksThePath(t *testing.T) {
	p := newOutboxBank(t)
	intake, processor := newInbox(p, statusAnswer(transfer.PartnerReturned))
	drainInbox(t, processor)

	created := p.sentTransfer(t, 2_500)
	returned := application.PartnerEvent{ID: newEventID(), Type: application.EventReturned, EndToEndID: created.EndToEndID, AmountCents: 2_500}
	settled := application.PartnerEvent{ID: newEventID(), Type: application.EventSettled, EndToEndID: created.EndToEndID, AmountCents: 2_500}

	receive(t, intake, returned, settled)
	drainInbox(t, processor)

	view := p.status(t, created.TransferID)
	isWalked := view.Transfer.Status == transfer.StatusReversed &&
		p.settlements(t, created.TransferID, "pix_out_settle") == 1 &&
		p.settlements(t, created.TransferID, "pix_out_return") == 1
	if !isWalked {
		t.Fatalf("status %s, %d settlements, %d returns", view.Transfer.Status, p.settlements(t, created.TransferID, "pix_out_settle"), p.settlements(t, created.TransferID, "pix_out_return"))
	}
}

// TestStateMachine_ContradictionIsAConflict: the bank confirmed a transfer
// the partner says it rejected. Nothing is undone automatically.
func TestStateMachine_ContradictionIsAConflict(t *testing.T) {
	p := newOutboxBank(t)
	intake, processor := newInbox(p, statusAnswer(transfer.PartnerRejected))
	drainInbox(t, processor)

	created := p.sentTransfer(t, 1_000)
	settle := application.TransitionCommand{TransferID: created.TransferID, From: transfer.StatusSent, To: transfer.StatusConfirmed, Cause: "test"}

	err := p.unitOfWork.Run(t.Context(), func(ctx context.Context, stores application.Stores) error {
		return application.GuardedTransitions{}.Move(ctx, stores, settle)
	})
	if err != nil {
		t.Fatal(err)
	}

	rejected := application.PartnerEvent{ID: newEventID(), Type: application.EventRejected, EndToEndID: created.EndToEndID, AmountCents: 1_000}
	receive(t, intake, rejected)
	drainInbox(t, processor)

	view := p.status(t, created.TransferID)
	isParked := view.Transfer.Status == transfer.StatusConfirmed && countRows(t, p, `SELECT count(*) FROM inbox_events WHERE event_id = $1 AND status = 'conflict'`, rejected.ID) == 1
	if !isParked {
		t.Fatalf("status %s; the contradiction should be a conflict", view.Transfer.Status)
	}
}

func (p pixBank) sweeper(partner application.PartnerStatusReader, owner string) application.Sweeper {
	config := application.SweeperConfig{UnitOfWork: p.unitOfWork, Machine: application.GuardedTransitions{}, Partner: partner, Owner: owner, Logger: discardLogger()}

	return application.NewSweeper(config)
}

// releaseSweeperLease lets each test start with a free lease.
func (p pixBank) releaseSweeperLease(t *testing.T) {
	t.Helper()

	_, err := p.bank.pool.Exec(t.Context(), `UPDATE worker_leases SET holder = NULL, expires_at = NULL WHERE resource = 'sweeper'`)
	if err != nil {
		t.Fatal(err)
	}
}

// TestSweeper_LostWebhook_TransferConvergesByPolling is PM-009: the partner
// settled and its webhooks were lost. The sweeper asks and confirms.
func TestSweeper_LostWebhook_TransferConvergesByPolling(t *testing.T) {
	p := newOutboxBank(t)
	p.releaseSweeperLease(t)
	created := p.sentTransfer(t, 3_300)

	err := p.sweeper(statusAnswer(transfer.PartnerSettled), "worker-a").RunOnce(t.Context(), sweptNow)
	if err != nil {
		t.Fatal(err)
	}

	view := p.status(t, created.TransferID)
	last := view.Transitions[len(view.Transitions)-1]

	isSwept := view.Transfer.Status == transfer.StatusConfirmed && last.Cause == "sweeper"
	if !isSwept {
		t.Fatalf("status %s, last cause %s", view.Transfer.Status, last.Cause)
	}
}

// TestSweeper_LeavesAnUnknownCreatedTransferToTheOutbox: not_found in
// created is the outbox's to resend; the sweeper never fails a transfer.
func TestSweeper_LeavesAnUnknownCreatedTransferToTheOutbox(t *testing.T) {
	p := newOutboxBank(t)
	p.releaseSweeperLease(t)
	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	created := p.createPixOut(t, account, 700)

	err := p.sweeper(statusAnswer(transfer.PartnerNotFound), "worker-a").RunOnce(t.Context(), sweptNow)
	if err != nil {
		t.Fatal(err)
	}

	if p.status(t, created.TransferID).Transfer.Status != transfer.StatusCreated {
		t.Fatal("the sweeper moved a transfer the partner does not know")
	}
}

// TestSweeper_OnlyTheLeaseHolderSweeps: a second worker finds the lease
// taken and does nothing.
func TestSweeper_OnlyTheLeaseHolderSweeps(t *testing.T) {
	p := newOutboxBank(t)
	p.releaseSweeperLease(t)

	err := p.sweeper(statusAnswer(transfer.PartnerProcessing), "worker-a").RunOnce(t.Context(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	created := p.sentTransfer(t, 900)

	err = p.sweeper(statusAnswer(transfer.PartnerSettled), "worker-b").RunOnce(t.Context(), sweptNow)
	if err != nil {
		t.Fatal(err)
	}

	if p.status(t, created.TransferID).Transfer.Status != transfer.StatusSent {
		t.Fatal("a worker without the lease swept")
	}
}

// TestRelay_PartnerTimeout_NeverFailsTransfer is PM-008 (FC21): with D9 a doubtful answer
// reschedules the message and refunds nothing; the next send, under the
// same end_to_end_id, gets the partner's answer.
func TestRelay_PartnerTimeout_NeverFailsTransfer(t *testing.T) {
	p := newOutboxBank(t)
	p.drainOutbox(t)
	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	created := p.createPixOut(t, account, 6_000)

	var calls atomic.Int32

	machine := application.GuardedTransitions{}
	flaky := application.OutboxRelayConfig{UnitOfWork: p.unitOfWork, Partner: fakePartner(t, http.StatusInternalServerError, &calls), EndToEnd: application.FixedEndToEnd{}, Machine: machine, Doubt: application.RetryOnDoubt{}, Owner: "relay-a", LeaseTTL: application.OutboxLeaseTTL, Logger: discardLogger()}

	err := application.NewOutboxRelay(flaky).RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if p.status(t, created.TransferID).Transfer.Status != transfer.StatusCreated {
		t.Fatal("a doubtful answer moved the transfer")
	}

	assertChainedAndNeverNegative(t, p.bank, account, 4_000)

	_, err = p.bank.pool.Exec(t.Context(), `UPDATE outbox_messages SET next_attempt_at = now() WHERE aggregate_id = $1`, uuid.UUID(created.TransferID))
	if err != nil {
		t.Fatal(err)
	}

	healthy := flaky
	healthy.Partner = fakePartner(t, http.StatusOK, &calls)

	err = application.NewOutboxRelay(healthy).RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if p.status(t, created.TransferID).Transfer.Status != transfer.StatusSent {
		t.Fatal("the resend after the doubt did not record the partner's answer")
	}
}

// pausingPartner answers with a status and, before the sweeper can use it,
// lets the webhook move the transfer on: a sweeper frozen at FP11.
type pausingPartner struct {
	answer   transfer.PartnerStatus
	meantime func()
}

func (p pausingPartner) Status(context.Context, string) (application.AuthoritativeStatus, error) {
	p.meantime()

	return application.AuthoritativeStatus{Status: p.answer}, nil
}

// TestSweeper_StaleStatusAfterPause_DoesNotRegressState is FC11: the
// sweeper read "processing", paused, and the settlement came in meanwhile.
// The walk starts from the status under the lock, so the stale answer
// writes nothing.
func TestSweeper_StaleStatusAfterPause_DoesNotRegressState(t *testing.T) {
	p := newOutboxBank(t)
	p.releaseSweeperLease(t)
	created := p.sentTransfer(t, 1_200)

	settleMeanwhile := func() {
		settle := application.TransitionCommand{TransferID: created.TransferID, From: transfer.StatusSent, To: transfer.StatusConfirmed, Cause: "webhook"}

		err := p.unitOfWork.Run(t.Context(), func(ctx context.Context, stores application.Stores) error {
			return application.GuardedTransitions{}.Move(ctx, stores, settle)
		})
		if err != nil {
			t.Error(err)
		}
	}

	stale := pausingPartner{answer: transfer.PartnerProcessing, meantime: sync.OnceFunc(settleMeanwhile)}

	err := p.sweeper(stale, "worker-a").RunOnce(t.Context(), sweptNow)
	if err != nil {
		t.Fatal(err)
	}

	view := p.status(t, created.TransferID)
	if view.Transfer.Status != transfer.StatusConfirmed || p.settlements(t, created.TransferID, "pix_out_settle") != 1 {
		t.Fatalf("status %s after a stale sweep", view.Transfer.Status)
	}
}
