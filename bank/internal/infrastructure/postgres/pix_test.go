package postgres_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/infrastructure/partnerclient"
	"centavo/bank/internal/infrastructure/postgres"
)

// pixBank is the naive PIX over the test database.
type pixBank struct {
	writes
	unitOfWork application.UnitOfWork
	create     application.CreatePixOut
	webhook    application.NaiveWebhook
	get        application.GetExternalTransfer
}

func newPixBank(t *testing.T) pixBank {
	t.Helper()

	w := newWrites(t)
	unitOfWork := postgres.NewUnitOfWork(w.bank.pool)
	publication := application.NewNotifyAfterCommit(postgres.NewNotifyPublisher(w.bank.pool))

	return pixBank{
		writes: w, unitOfWork: unitOfWork, create: application.NewCreatePixOut(application.RowLocker{}, publication),
		webhook: application.NewNaiveWebhook(unitOfWork, application.OverwriteTransitions{}), get: application.NewGetExternalTransfer(postgres.NewLedgerStore(w.bank.pool)),
	}
}

func (p pixBank) createPixOut(t *testing.T, account ledger.AccountID, cents int64) application.PixOutResult {
	t.Helper()

	key := newKey()
	input := application.PixOutInput{AccountID: account, Amount: amountOf(t, money.Cents(cents)), DestinationKey: "dest-0001", Write: application.WriteContext{Key: key, CorrelationID: string(key)}}

	var result application.PixOutResult

	err := p.unitOfWork.Run(t.Context(), func(ctx context.Context, stores application.Stores) error {
		operationID, err := ledger.NewOperationID()
		if err != nil {
			return err
		}

		result, err = p.create.Apply(ctx, stores, operationID, input)

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	return result
}

func (p pixBank) status(t *testing.T, id transfer.ID) application.ExternalTransferView {
	t.Helper()

	view, err := p.get.Execute(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}

	return view
}

// fakePartner answers every submission with the same status code.
func fakePartner(t *testing.T, status int, calls *atomic.Int32) partnerclient.Client {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)

	return partnerclient.New(server.URL, "centavo-robust")
}

func TestPixOut_ReservesTheAmountInTransit(t *testing.T) {
	p := newPixBank(t)
	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)

	result := p.createPixOut(t, account, 4_500)

	view := p.status(t, result.TransferID)
	isCreated := view.Transfer.Status == transfer.StatusCreated && len(view.Transitions) == 1 && view.Transitions[0].From == nil
	if !isCreated {
		t.Fatalf("transfer: %+v", view)
	}

	assertChainedAndNeverNegative(t, p.bank, account, 5_500)
}

func TestNaiveRelay_RecordsWhatThePartnerAnswered(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		want        transfer.Status
		wantRefund  bool
		wantFailure string
	}{
		{name: "accepted", status: http.StatusAccepted, want: transfer.StatusSent},
		{name: "rejected", status: http.StatusUnprocessableEntity, want: transfer.StatusFailed, wantRefund: true, wantFailure: "partner_rejected"},
		// The naive relay takes doubt for failure and refunds: if the partner
		// did pay, the bank just created money.
		{name: "unknown", status: http.StatusInternalServerError, want: transfer.StatusFailed, wantRefund: true, wantFailure: "partner_timeout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newPixBank(t)
			account := p.bank.newCustomer(t)
			p.bank.fund(t, account, 10_000)
			created := p.createPixOut(t, account, 4_500)

			var calls atomic.Int32

			relay := application.NewNaiveRelay(p.unitOfWork, fakePartner(t, tc.status, &calls), application.OverwriteTransitions{})

			err := relay.Relay(t.Context(), created.TransferID)
			if err != nil {
				t.Fatal(err)
			}

			view := p.status(t, created.TransferID)
			if view.Transfer.Status != tc.want || view.Transfer.FailureCode != tc.wantFailure {
				t.Fatalf("status %s (%q), want %s (%q)", view.Transfer.Status, view.Transfer.FailureCode, tc.want, tc.wantFailure)
			}

			wantBalance := int64(5_500)
			if tc.wantRefund {
				wantBalance = 10_000
			}

			assertChainedAndNeverNegative(t, p.bank, account, wantBalance)

			err = relay.Relay(t.Context(), created.TransferID)
			if err != nil || calls.Load() != 1 {
				t.Fatalf("a transfer past created was sent again (%d calls, %v)", calls.Load(), err)
			}
		})
	}
}

// TestNaiveWebhook_AppliesDuplicatesTwice guards a known failure: without the
// inbox and the guarded state machine, the same event settles a transfer twice.
func TestNaiveWebhook_AppliesDuplicatesTwice(t *testing.T) {
	p := newPixBank(t)
	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	created := p.createPixOut(t, account, 4_500)
	settled := application.PartnerEvent{ID: "evt_1", Type: application.EventSettled, EndToEndID: created.EndToEndID, AmountCents: 4_500}

	for range 2 {
		err := p.webhook.Apply(t.Context(), settled)
		if err != nil {
			t.Fatal(err)
		}
	}

	view := p.status(t, created.TransferID)
	if view.Transfer.Status != transfer.StatusConfirmed || len(view.Transitions) != 3 {
		t.Fatalf("transfer: %s with %d transitions", view.Transfer.Status, len(view.Transitions))
	}

	var settlements int

	err := p.bank.pool.QueryRow(t.Context(), `SELECT count(*) FROM ledger_transactions WHERE external_transfer_id = $1 AND kind = 'pix_out_settle'`, uuid.UUID(created.TransferID)).Scan(&settlements)
	if err != nil || settlements != 2 {
		t.Fatalf("settlements: %d, %v", settlements, err)
	}
}

func TestNaiveWebhook_CreditsAnIncomingPixOnce(t *testing.T) {
	p := newPixBank(t)
	account := p.bank.newCustomer(t)
	incoming := application.PartnerEvent{ID: "evt_in", Type: application.EventIncoming, EndToEndID: transfer.EndToEndID("E" + uuid.NewString()[:8] + "0000aaaa"), AmountCents: 900, AccountID: account.String()}

	err := p.webhook.Apply(t.Context(), incoming)
	if err != nil {
		t.Fatal(err)
	}

	err = p.webhook.Apply(t.Context(), incoming)
	if !errors.Is(err, application.ErrAlreadyCredited) {
		t.Fatalf("second credit: got %v", err)
	}

	assertChainedAndNeverNegative(t, p.bank, account, 900)
}

func TestNotify_ReachesAListener(t *testing.T) {
	p := newPixBank(t)
	received := make(chan string, 1)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	listener := postgres.Listener{Pool: p.bank.pool, Channel: postgres.PixOutChannel, Handle: func(_ context.Context, payload string) { received <- payload }, Logger: discardLogger()}
	go listener.Run(ctx)

	id, err := transfer.NewID()
	if err != nil {
		t.Fatal(err)
	}

	publisher := postgres.NewNotifyPublisher(p.bank.pool)

	for {
		err = publisher.Publish(ctx, id)
		if err != nil {
			t.Fatal(err)
		}

		select {
		case payload := <-received:
			if payload != id.String() {
				t.Fatalf("payload %q", payload)
			}

			return
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("no notification arrived; the listener may not be listening yet")
		}
	}
}
