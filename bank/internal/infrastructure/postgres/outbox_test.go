package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/infrastructure/partnerclient"
)

const expiredLease = time.Millisecond

type recordingPartner struct {
	mu          sync.Mutex
	endToEndIDs []transfer.EndToEndID
}

// dyingAfterSubmit lets the submission reach the partner and then ends the
// relay's context, so nothing it does after the call is saved: a kill at
// `relay.after_partner_call_before_save` without the chaoslab build.
type dyingAfterSubmit struct {
	partner application.PartnerSubmitter
	die     context.CancelFunc
}

type statusAnswer transfer.PartnerStatus

// TestPixOut_CrashAfterSave_IsStillSubmitted is a regression test: the API died
// after the commit (`pixout.after_save_before_publish`), so nothing was
// published, and the transfer still goes.
func TestPixOut_CrashAfterSave_IsStillSubmitted(t *testing.T) {
	fixture := newOutboxBank(t)
	fixture.drainOutbox(t)
	account := fixture.bank.newCustomer(t)
	fixture.bank.fund(t, account, 10_000)

	created := fixture.createPixOut(t, account, 4_500)

	partner, client := newRecordingPartner(t)
	relay := fixture.relay(client, application.FixedEndToEnd{}, application.OutboxLeaseTTL)

	for range 2 {
		err := relay.RunOnce(t.Context())
		if err != nil {
			t.Fatal(err)
		}
	}

	calls := partner.calls()
	isUnexpected := len(calls) != 1 || calls[0] != created.EndToEndID
	if isUnexpected {
		t.Fatalf("partner calls: %v, want one with %s", calls, created.EndToEndID)
	}

	view := fixture.status(t, created.TransferID)
	if view.Transfer.Status != transfer.StatusSent {
		t.Fatalf("status %s, want sent", view.Transfer.Status)
	}
}

// TestOutbox_StaleTokenCannotFinish is the fencing of the relay lease: a
// relay that lost its lease while it was away cannot close the message.
func TestOutbox_StaleTokenCannotFinish(t *testing.T) {
	fixture := newOutboxBank(t)
	fixture.drainOutbox(t)
	account := fixture.bank.newCustomer(t)
	fixture.bank.fund(t, account, 10_000)
	fixture.createPixOut(t, account, 1_000)

	zombie := fixture.claim(t, "zombie", expiredLease)
	time.Sleep(10 * expiredLease)
	successor := fixture.claim(t, "successor", time.Minute)

	isSameMessage := len(zombie) == 1 && len(successor) == 1 && zombie[0].ID == successor[0].ID
	if !isSameMessage {
		t.Fatalf("claims: zombie %+v, successor %+v", zombie, successor)
	}

	if successor[0].Token <= zombie[0].Token {
		t.Fatalf("token %d after %d: tokens must grow", successor[0].Token, zombie[0].Token)
	}

	if fixture.finish(t, zombie[0]) {
		t.Fatal("the zombie's stale token closed the message")
	}

	if !fixture.finish(t, successor[0]) {
		t.Fatal("the current token could not close the message")
	}
}

// TestRelay_RedeliveryAfterCrash_ReusesEndToEndID is a regression test: the
// relay died after the partner took the PIX
// (`relay.after_partner_call_before_save`). With partner_idempotency the next
// relay sends the same end_to_end_id, which the partner answers without paying
// again; the naive id per attempt is a second PIX.
func TestRelay_RedeliveryAfterCrash_ReusesEndToEndID(t *testing.T) {
	cases := []struct {
		name         string
		policy       application.EndToEndPolicy
		shouldKeepID bool
	}{
		{name: "fixed", policy: application.FixedEndToEnd{}, shouldKeepID: true},
		{name: "per attempt", policy: application.NewEndToEndPerAttempt{}, shouldKeepID: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newOutboxBank(t)
			fixture.drainOutbox(t)
			account := fixture.bank.newCustomer(t)
			fixture.bank.fund(t, account, 10_000)
			created := fixture.createPixOut(t, account, 2_000)

			partner, client := newRecordingPartner(t)

			// The first relay sends and dies before saving: its lease is
			// left to expire.
			ctx, die := context.WithCancel(t.Context())
			dying := fixture.relay(dyingAfterSubmit{partner: client, die: die}, tc.policy, expiredLease)

			err := dying.RunOnce(ctx)
			if err == nil {
				t.Fatal("the dying relay saved the answer")
			}

			time.Sleep(10 * expiredLease)

			err = fixture.relay(client, tc.policy, application.OutboxLeaseTTL).RunOnce(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			calls := partner.calls()
			if len(calls) != 2 {
				t.Fatalf("partner calls: %v, want the first send and the retry", calls)
			}

			isSameID := calls[0] == calls[1]
			if isSameID != tc.shouldKeepID {
				t.Fatalf("end_to_end_ids %v, same = %v, want %v", calls, isSameID, tc.shouldKeepID)
			}

			view := fixture.status(t, created.TransferID)
			if view.Transfer.Status != transfer.StatusSent {
				t.Fatalf("status %s, want sent", view.Transfer.Status)
			}
		})
	}
}

// TestOutbox_AnswerDoesNotOverwriteAWebhook: the partner's settlement came
// in before the relay got to the message, so the relay only closes it.
func TestOutbox_AnswerDoesNotOverwriteAWebhook(t *testing.T) {
	fixture := newOutboxBank(t)
	fixture.drainOutbox(t)
	account := fixture.bank.newCustomer(t)
	fixture.bank.fund(t, account, 10_000)
	created := fixture.createPixOut(t, account, 3_000)

	settled := application.PartnerEvent{ID: "evt_" + uuid.NewString(), Type: application.EventSettled, EndToEndID: created.EndToEndID, AmountCents: 3_000}

	err := fixture.webhook.Apply(t.Context(), settled)
	if err != nil {
		t.Fatal(err)
	}

	partner, client := newRecordingPartner(t)

	err = fixture.relay(client, application.FixedEndToEnd{}, application.OutboxLeaseTTL).RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	view := fixture.status(t, created.TransferID)
	isUntouched := view.Transfer.Status == transfer.StatusConfirmed && len(partner.calls()) == 0
	if !isUntouched {
		t.Fatalf("status %s after %d partner calls", view.Transfer.Status, len(partner.calls()))
	}
}

// TestInbox_DuplicateEventID_IsIgnored is a regression test: the API died
// before the 200 (`webhook.after_receive_before_ack`), the partner delivered
// the same event again, and the transfer is settled once.
func TestInbox_DuplicateEventID_IsIgnored(t *testing.T) {
	fixture := newOutboxBank(t)
	intake, processor := newInbox(fixture, statusAnswer(transfer.PartnerSettled))
	drainInbox(t, processor)

	account := fixture.bank.newCustomer(t)
	fixture.bank.fund(t, account, 10_000)
	created := fixture.createPixOut(t, account, 4_500)
	settled := application.PartnerEvent{ID: "evt_" + uuid.NewString(), Type: application.EventSettled, EndToEndID: created.EndToEndID, AmountCents: 4_500}

	for range 3 {
		err := intake.Apply(t.Context(), settled)
		if err != nil {
			t.Fatal(err)
		}
	}

	if countRows(t, fixture, `SELECT count(*) FROM inbox_events WHERE event_id = $1`, settled.ID) != 1 {
		t.Fatal("the inbox kept more than one copy of the event")
	}

	drainInbox(t, processor)

	settlements := countRows(t, fixture, `SELECT count(*) FROM ledger_transactions WHERE external_transfer_id = $1 AND kind = 'pix_out_settle'`, uuid.UUID(created.TransferID))
	view := fixture.status(t, created.TransferID)

	isSettledOnce := settlements == 1 && view.Transfer.Status == transfer.StatusConfirmed
	if !isSettledOnce {
		t.Fatalf("%d settlements, status %s", settlements, view.Transfer.Status)
	}
}

// TestInbox_SecondCreditOfAPixInIsADuplicate: two events for one PIX in
// credit it once, and the second is closed, not retried forever.
func TestInbox_SecondCreditOfAPixInIsADuplicate(t *testing.T) {
	fixture := newOutboxBank(t)
	intake, processor := newInbox(fixture, nil)
	drainInbox(t, processor)

	account := fixture.bank.newCustomer(t)
	endToEndID := transfer.EndToEndID("E" + uuid.NewString()[:8] + "0000bbbb")

	for _, eventID := range []string{"evt_" + uuid.NewString(), "evt_" + uuid.NewString()} {
		incoming := application.PartnerEvent{ID: eventID, Type: application.EventIncoming, EndToEndID: endToEndID, AmountCents: 700, AccountID: account.String()}

		err := intake.Apply(t.Context(), incoming)
		if err != nil {
			t.Fatal(err)
		}
	}

	drainInbox(t, processor)

	assertChainedAndNeverNegative(t, fixture.bank, account, 700)

	outcomes := countRows(t, fixture, `SELECT count(*) FROM inbox_events WHERE end_to_end_id = $1 AND status = 'processed' AND outcome = 'duplicate'`, endToEndID)
	if outcomes != 1 {
		t.Fatalf("%d events marked duplicate, want 1", outcomes)
	}
}

// TestInbox_EventForAnUnknownTransferIsAConflict: the processor parks what it
// can never apply instead of retrying it.
func TestInbox_EventForAnUnknownTransferIsAConflict(t *testing.T) {
	fixture := newOutboxBank(t)
	intake, processor := newInbox(fixture, nil)
	drainInbox(t, processor)

	id, err := transfer.NewID()
	if err != nil {
		t.Fatal(err)
	}

	orphan := application.PartnerEvent{ID: "evt_" + uuid.NewString(), Type: application.EventSettled, EndToEndID: id.EndToEndID(), AmountCents: 100}

	err = intake.Apply(t.Context(), orphan)
	if err != nil {
		t.Fatal(err)
	}

	drainInbox(t, processor)

	if countRows(t, fixture, `SELECT count(*) FROM inbox_events WHERE event_id = $1 AND status = 'conflict'`, orphan.ID) != 1 {
		t.Fatal("the orphan event is not in conflict")
	}
}

func newRecordingPartner(t *testing.T) (*recordingPartner, partnerclient.Client) {
	t.Helper()

	partner := &recordingPartner{}
	server := httptest.NewServer(http.HandlerFunc(partner.serve))
	t.Cleanup(server.Close)

	return partner, partnerclient.New(server.URL, "centavo-robust")
}

func (p *recordingPartner) serve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		EndToEndID transfer.EndToEndID `json:"end_to_end_id"`
	}

	err := json.NewDecoder(r.Body).Decode(&body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)

		return
	}

	p.mu.Lock()
	p.endToEndIDs = append(p.endToEndIDs, body.EndToEndID)
	p.mu.Unlock()

	w.WriteHeader(http.StatusAccepted)
}

func (p *recordingPartner) calls() []transfer.EndToEndID {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]transfer.EndToEndID{}, p.endToEndIDs...)
}

func newOutboxBank(t *testing.T) pixBank {
	t.Helper()

	fixture := newPixBank(t)
	fixture.create = application.NewCreatePixOut(application.RowLocker{}, application.OutboxPublication{})

	return fixture
}

func (p pixBank) relay(partner application.PartnerSubmitter, endToEnd application.EndToEndPolicy, leaseTTL time.Duration) application.OutboxRelay {
	machine := application.GuardedTransitions{}
	config := application.OutboxRelayConfig{UnitOfWork: p.unitOfWork, Partner: partner, EndToEnd: endToEnd, Machine: machine, Doubt: application.NewGiveUpOnDoubt(machine), Owner: "relay-" + uuid.NewString()[:8], LeaseTTL: leaseTTL, Logger: discardLogger()}

	return application.NewOutboxRelay(config)
}

func (p pixBank) claim(t *testing.T, owner string, ttl time.Duration) []application.OutboxClaim {
	t.Helper()

	var claims []application.OutboxClaim

	err := p.unitOfWork.Run(t.Context(), func(ctx context.Context, stores application.Stores) error {
		var err error

		claims, err = stores.ClaimOutbox(ctx, owner, ttl, 100)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return claims
}

func (p pixBank) finish(t *testing.T, claim application.OutboxClaim) bool {
	t.Helper()

	var isOurs bool

	err := p.unitOfWork.Run(t.Context(), func(ctx context.Context, stores application.Stores) error {
		var err error

		isOurs, err = stores.FinishOutbox(ctx, claim.ID, claim.Token)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return isOurs
}

// drainOutbox finishes whatever earlier tests left pending, so each test
// sees only its own messages.
func (p pixBank) drainOutbox(t *testing.T) {
	t.Helper()

	for _, claim := range p.claim(t, "drain", time.Minute) {
		p.finish(t, claim)
	}
}

func (d dyingAfterSubmit) Submit(ctx context.Context, submission application.PartnerSubmission) application.PartnerAnswer {
	answer := d.partner.Submit(ctx, submission)
	d.die()

	return answer
}

func (p pixBank) markSent(t *testing.T, id transfer.ID) {
	t.Helper()

	command := application.TransitionCommand{TransferID: id, From: transfer.StatusCreated, To: transfer.StatusSent, Cause: "relay"}

	err := p.unitOfWork.Run(t.Context(), func(ctx context.Context, stores application.Stores) error {
		return application.GuardedTransitions{}.Move(ctx, stores, command)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (a statusAnswer) Status(context.Context, transfer.EndToEndID) (application.AuthoritativeStatus, error) {
	return application.AuthoritativeStatus{Status: transfer.PartnerStatus(a)}, nil
}

func newInbox(fixture pixBank, partner application.PartnerStatusReader) (application.InboxIntake, application.InboxProcessor) {
	config := application.InboxProcessorConfig{UnitOfWork: fixture.unitOfWork, Machine: application.GuardedTransitions{}, Partner: partner, Logger: discardLogger()}

	return application.NewInboxIntake(fixture.unitOfWork), application.NewInboxProcessor(config)
}

func drainInbox(t *testing.T, processor application.InboxProcessor) {
	t.Helper()

	for {
		hasMore, err := processor.RunOnce(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if !hasMore {
			return
		}
	}
}

func countRows(t *testing.T, fixture pixBank, query string, arguments ...any) int {
	t.Helper()

	var count int

	err := fixture.bank.pool.QueryRow(t.Context(), query, arguments...).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	return count
}
