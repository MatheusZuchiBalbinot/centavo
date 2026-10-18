package partnerclient

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"centavo/bank/internal/application"
)

var anySubmission = application.PartnerSubmission{EndToEndID: "E0001", AmountCents: 100, DestinationKey: "dest-0001"}

// switchablePartner answers 500 until it is told to recover.
type switchablePartner struct {
	calls       atomic.Int32
	isRecovered atomic.Bool
}

func TestBreaker_OpensAfterAWindowOfFailuresAndStopsCalling(t *testing.T) {
	breaker, partner, _ := newTestBreaker(t)

	for range breakerWindow {
		breaker.Submit(t.Context(), anySubmission)
	}

	callsWhenOpened := partner.calls.Load()

	answer := breaker.Submit(t.Context(), anySubmission)
	isUnexpected := answer != application.PartnerUnknown || partner.calls.Load() != callsWhenOpened
	if isUnexpected {
		t.Fatalf("an open breaker called the partner (%d calls, answer %v)", partner.calls.Load(), answer)
	}

	_, err := breaker.Status(t.Context(), "E0001")
	if err == nil {
		t.Fatal("an open breaker answered a status query")
	}
}

func TestBreaker_HalfOpenProbeClosesItAgain(t *testing.T) {
	breaker, partner, now := newTestBreaker(t)

	for range breakerWindow {
		breaker.Submit(t.Context(), anySubmission)
	}

	partner.isRecovered.Store(true)
	*now = now.Add(breakerOpenFor)

	answer := breaker.Submit(t.Context(), anySubmission)
	if answer != application.PartnerAccepted {
		t.Fatalf("the probe got %v", answer)
	}

	answer = breaker.Submit(t.Context(), anySubmission)
	if answer != application.PartnerAccepted {
		t.Fatalf("after a good probe the breaker answered %v", answer)
	}
}

func TestBreaker_FailedProbeReopens(t *testing.T) {
	breaker, partner, now := newTestBreaker(t)

	for range breakerWindow {
		breaker.Submit(t.Context(), anySubmission)
	}

	*now = now.Add(breakerOpenFor)
	breaker.Submit(t.Context(), anySubmission)

	callsAfterProbe := partner.calls.Load()
	breaker.Submit(t.Context(), anySubmission)

	if partner.calls.Load() != callsAfterProbe {
		t.Fatal("a failed probe left the breaker closed")
	}
}

func TestBreaker_MostlyHealthyPartnerKeepsItClosed(t *testing.T) {
	breaker, partner, _ := newTestBreaker(t)
	partner.isRecovered.Store(true)

	for range 3 * breakerWindow {
		breaker.Submit(t.Context(), anySubmission)
	}

	if partner.calls.Load() != 3*breakerWindow {
		t.Fatalf("%d calls reached a healthy partner, want %d", partner.calls.Load(), 3*breakerWindow)
	}
}

func (p *switchablePartner) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	p.calls.Add(1)

	if p.isRecovered.Load() {
		w.WriteHeader(http.StatusAccepted)

		return
	}

	w.WriteHeader(http.StatusInternalServerError)
}

func newTestBreaker(t *testing.T) (*Breaker, *switchablePartner, *time.Time) {
	t.Helper()

	partner := &switchablePartner{}
	server := httptest.NewServer(partner)
	t.Cleanup(server.Close)

	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	breaker := NewBreaker(New(server.URL, "centavo-robust"))
	breaker.now = func() time.Time { return now }

	return breaker, partner, &now
}
