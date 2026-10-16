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

// expiredLease is a lease that ends before the next claim looks at it.
const expiredLease = time.Millisecond

// recordingPartner accepts every PIX and keeps the end_to_end_id of each call.
type recordingPartner struct {
	mu          sync.Mutex
	endToEndIDs []string
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
		EndToEndID string `json:"end_to_end_id"`
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

func (p *recordingPartner) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string{}, p.endToEndIDs...)
}

// newOutboxBank is the PIX of phase F7: the transfer and its outbox message
// share a commit.
func newOutboxBank(t *testing.T) pixBank {
	t.Helper()

	p := newPixBank(t)
	p.create = application.NewCreatePixOut(application.RowLocker{}, application.OutboxPublication{})

	return p
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

// TestOutbox_DeliversATransferNobodyAnnounced is PM-004: the API died after
// the commit (FP04), so nothing was published, and the transfer still goes.
func TestOutbox_DeliversATransferNobodyAnnounced(t *testing.T) {
	p := newOutboxBank(t)
	p.drainOutbox(t)
	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)

	created := p.createPixOut(t, account, 4_500)

	partner, client := newRecordingPartner(t)
	relay := p.relay(client, application.FixedEndToEnd{}, application.OutboxLeaseTTL)

	for range 2 {
		err := relay.RunOnce(t.Context())
		if err != nil {
			t.Fatal(err)
		}
	}

	calls := partner.calls()
	if len(calls) != 1 || calls[0] != created.EndToEndID {
		t.Fatalf("partner calls: %v, want one with %s", calls, created.EndToEndID)
	}

	view := p.status(t, created.TransferID)
	if view.Transfer.Status != transfer.StatusSent {
		t.Fatalf("status %s, want sent", view.Transfer.Status)
	}
}

// TestOutbox_StaleTokenCannotFinish is the fencing of the relay lease: a
// relay that lost its lease while it was away cannot close the message.
func TestOutbox_StaleTokenCannotFinish(t *testing.T) {
	p := newOutboxBank(t)
	p.drainOutbox(t)
	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	p.createPixOut(t, account, 1_000)

	zombie := p.claim(t, "zombie", expiredLease)
	time.Sleep(10 * expiredLease)
	successor := p.claim(t, "successor", time.Minute)

	isSameMessage := len(zombie) == 1 && len(successor) == 1 && zombie[0].ID == successor[0].ID
	if !isSameMessage {
		t.Fatalf("claims: zombie %+v, successor %+v", zombie, successor)
	}

	if successor[0].Token <= zombie[0].Token {
		t.Fatalf("token %d after %d: tokens must grow", successor[0].Token, zombie[0].Token)
	}

	if p.finish(t, zombie[0]) {
		t.Fatal("the zombie's stale token closed the message")
	}

	if !p.finish(t, successor[0]) {
		t.Fatal("the current token could not close the message")
	}
}

// TestOutbox_RetryAfterACrashReusesTheEndToEndID is PM-005: the relay died
// after the partner took the PIX (FP03). With D6 the next relay sends the
// same end_to_end_id, which the partner answers without paying again; the
// naive id per attempt is a second PIX.
func TestOutbox_RetryAfterACrashReusesTheEndToEndID(t *testing.T) {
	cases := []struct {
		name       string
		policy     application.EndToEndPolicy
		wantSameID bool
	}{
		{name: "fixed", policy: application.FixedEndToEnd{}, wantSameID: true},
		{name: "per attempt", policy: application.NewEndToEndPerAttempt{}, wantSameID: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newOutboxBank(t)
			p.drainOutbox(t)
			account := p.bank.newCustomer(t)
			p.bank.fund(t, account, 10_000)
			created := p.createPixOut(t, account, 2_000)

			partner, client := newRecordingPartner(t)

			// The first relay sends and dies before saving: its lease is
			// left to expire.
			ctx, die := context.WithCancel(t.Context())
			dying := p.relay(dyingAfterSubmit{partner: client, die: die}, tc.policy, expiredLease)

			err := dying.RunOnce(ctx)
			if err == nil {
				t.Fatal("the dying relay saved the answer")
			}

			time.Sleep(10 * expiredLease)

			err = p.relay(client, tc.policy, application.OutboxLeaseTTL).RunOnce(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			calls := partner.calls()
			if len(calls) != 2 {
				t.Fatalf("partner calls: %v, want the first send and the retry", calls)
			}

			isSameID := calls[0] == calls[1]
			if isSameID != tc.wantSameID {
				t.Fatalf("end_to_end_ids %v, same = %v, want %v", calls, isSameID, tc.wantSameID)
			}

			view := p.status(t, created.TransferID)
			if view.Transfer.Status != transfer.StatusSent {
				t.Fatalf("status %s, want sent", view.Transfer.Status)
			}
		})
	}
}

// dyingAfterSubmit lets the submission reach the partner and then ends the
// relay's context, so nothing it does after the call is saved: a kill at FP03
// without the chaoslab build.
type dyingAfterSubmit struct {
	partner application.PartnerSubmitter
	die     context.CancelFunc
}

func (d dyingAfterSubmit) Submit(ctx context.Context, submission application.PartnerSubmission) application.PartnerAnswer {
	answer := d.partner.Submit(ctx, submission)
	d.die()

	return answer
}

// TestOutbox_AnswerDoesNotOverwriteAWebhook: the partner's settlement came
// in before the relay got to the message, so the relay only closes it.
func TestOutbox_AnswerDoesNotOverwriteAWebhook(t *testing.T) {
	p := newOutboxBank(t)
	p.drainOutbox(t)
	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	created := p.createPixOut(t, account, 3_000)

	settled := application.PartnerEvent{ID: "evt_" + uuid.NewString(), Type: application.EventSettled, EndToEndID: created.EndToEndID, AmountCents: 3_000}

	err := p.webhook.Apply(t.Context(), settled)
	if err != nil {
		t.Fatal(err)
	}

	partner, client := newRecordingPartner(t)

	err = p.relay(client, application.FixedEndToEnd{}, application.OutboxLeaseTTL).RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	view := p.status(t, created.TransferID)
	isUntouched := view.Transfer.Status == transfer.StatusConfirmed && len(partner.calls()) == 0
	if !isUntouched {
		t.Fatalf("status %s after %d partner calls", view.Transfer.Status, len(partner.calls()))
	}
}

// markSent records the partner's acceptance, as the relay would.
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

// statusAnswer is a partner that answers every status query the same way.
type statusAnswer transfer.PartnerStatus

func (a statusAnswer) Status(context.Context, string) (application.AuthoritativeStatus, error) {
	return application.AuthoritativeStatus{Status: transfer.PartnerStatus(a)}, nil
}

// newInbox is D5 over the test database.
func newInbox(p pixBank, partner application.PartnerStatusReader) (application.InboxIntake, application.InboxProcessor) {
	config := application.InboxProcessorConfig{UnitOfWork: p.unitOfWork, Machine: application.GuardedTransitions{}, Partner: partner, Logger: discardLogger()}

	return application.NewInboxIntake(p.unitOfWork), application.NewInboxProcessor(config)
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

func countRows(t *testing.T, p pixBank, query string, args ...any) int {
	t.Helper()

	var count int

	err := p.bank.pool.QueryRow(t.Context(), query, args...).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	return count
}

// TestInbox_DuplicateEventID_IsIgnored is PM-006: the API died before the
// 200 (FP05), the partner delivered the same event again, and the transfer
// is settled once.
func TestInbox_DuplicateEventID_IsIgnored(t *testing.T) {
	p := newOutboxBank(t)
	intake, processor := newInbox(p, statusAnswer(transfer.PartnerSettled))
	drainInbox(t, processor)

	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	created := p.createPixOut(t, account, 4_500)
	settled := application.PartnerEvent{ID: "evt_" + uuid.NewString(), Type: application.EventSettled, EndToEndID: created.EndToEndID, AmountCents: 4_500}

	for range 3 {
		err := intake.Apply(t.Context(), settled)
		if err != nil {
			t.Fatal(err)
		}
	}

	if countRows(t, p, `SELECT count(*) FROM inbox_events WHERE event_id = $1`, settled.ID) != 1 {
		t.Fatal("the inbox kept more than one copy of the event")
	}

	drainInbox(t, processor)

	settlements := countRows(t, p, `SELECT count(*) FROM ledger_transactions WHERE external_transfer_id = $1 AND kind = 'pix_out_settle'`, uuid.UUID(created.TransferID))
	view := p.status(t, created.TransferID)

	isSettledOnce := settlements == 1 && view.Transfer.Status == transfer.StatusConfirmed
	if !isSettledOnce {
		t.Fatalf("%d settlements, status %s", settlements, view.Transfer.Status)
	}
}

// TestInbox_SecondCreditOfAPixInIsADuplicate: two events for one PIX in
// credit it once, and the second is closed, not retried forever.
func TestInbox_SecondCreditOfAPixInIsADuplicate(t *testing.T) {
	p := newOutboxBank(t)
	intake, processor := newInbox(p, nil)
	drainInbox(t, processor)

	account := p.bank.newCustomer(t)
	endToEndID := "E" + uuid.NewString()[:8] + "0000bbbb"

	for _, eventID := range []string{"evt_" + uuid.NewString(), "evt_" + uuid.NewString()} {
		incoming := application.PartnerEvent{ID: eventID, Type: application.EventIncoming, EndToEndID: endToEndID, AmountCents: 700, AccountID: account.String()}

		err := intake.Apply(t.Context(), incoming)
		if err != nil {
			t.Fatal(err)
		}
	}

	drainInbox(t, processor)

	assertChainedAndNeverNegative(t, p.bank, account, 700)

	outcomes := countRows(t, p, `SELECT count(*) FROM inbox_events WHERE end_to_end_id = $1 AND status = 'processed' AND outcome = 'duplicate'`, endToEndID)
	if outcomes != 1 {
		t.Fatalf("%d events marked duplicate, want 1", outcomes)
	}
}

// TestInbox_EventForAnUnknownTransferIsAConflict: the processor parks what it
// can never apply instead of retrying it.
func TestInbox_EventForAnUnknownTransferIsAConflict(t *testing.T) {
	p := newOutboxBank(t)
	intake, processor := newInbox(p, nil)
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

	if countRows(t, p, `SELECT count(*) FROM inbox_events WHERE event_id = $1 AND status = 'conflict'`, orphan.ID) != 1 {
		t.Fatal("the orphan event is not in conflict")
	}
}
