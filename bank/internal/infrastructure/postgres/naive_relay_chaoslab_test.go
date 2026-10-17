//go:build chaoslab

package postgres_test

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/failpoint"
)

// TestNaiveRelay_FailpointMatchesTheTransfersClientRef guards FP03 in the
// naive relay: the lab arms it for the round's correlation ids, so a relay
// without the transfer's client_ref in its context never reaches the window.
func TestNaiveRelay_FailpointMatchesTheTransfersClientRef(t *testing.T) {
	p := newPixBank(t)
	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	created := p.createPixOut(t, account, 3_000)
	clientRef := p.status(t, created.TransferID).Transfer.ClientRef

	failpoint.DisarmAll()
	failpoint.ConfigureEvents(failpoint.EventsConfig{Dir: t.TempDir(), Service: "bank-worker", Instance: "test"})
	t.Cleanup(failpoint.DisarmAll)

	rule := failpoint.Rule{Action: failpoint.ActionError, Arg: "crash", Probability: 1, Match: failpoint.Match{CorrelationID: clientRef}, TTL: "1m"}

	err := failpoint.Arm(failpoint.RelayAfterPartnerCallBeforeSave, rule, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32

	relay := application.NewNaiveRelay(p.unitOfWork, fakePartner(t, http.StatusAccepted, &calls))

	err = relay.Relay(t.Context(), created.TransferID)
	if !errors.Is(err, failpoint.ErrSynthetic) {
		t.Fatalf("relay error %v, want the injected one", err)
	}

	view := p.status(t, created.TransferID)
	isStuckAfterTheCall := calls.Load() == 1 && view.Transfer.Status == transfer.StatusCreated
	if !isStuckAfterTheCall {
		t.Fatalf("%d calls, status %s: FP03 did not stop the relay after the partner call", calls.Load(), view.Transfer.Status)
	}
}
