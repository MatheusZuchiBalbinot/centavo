package partnerclient

import (
	"context"
	"errors"
	"sync"
	"time"

	"centavo/bank/internal/application"
)

// Circuit breaker settings (docs/architecture.md, section 7).
const (
	breakerWindow     = 20
	breakerOpenFor    = 10 * time.Second
	breakerFailureNum = 1
	breakerFailureDen = 2
)

// ErrBreakerOpen is a call the breaker did not let through.
var ErrBreakerOpen = errors.New("partner circuit breaker is open")

type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

// Breaker is defense D9's circuit breaker around the partner: it opens when
// half of the last 20 calls failed, stays open for 10 s and then lets one
// probe through. An open breaker answers "unknown" without calling, so the
// relay reschedules and nothing is failed (BR10).
type Breaker struct {
	client Client
	now    func() time.Time

	mu           sync.Mutex
	state        breakerState
	outcomes     []bool
	openedAt     time.Time
	isProbeOnAir bool
}

// NewBreaker wraps a client.
func NewBreaker(client Client) *Breaker {
	return &Breaker{client: client, now: time.Now}
}

// Submit sends the PIX if the breaker allows it.
func (b *Breaker) Submit(ctx context.Context, submission application.PartnerSubmission) application.PartnerAnswer {
	if !b.allow() {
		return application.PartnerUnknown
	}

	answer := b.client.Submit(ctx, submission)
	b.record(answer != application.PartnerUnknown)

	return answer
}

// Status queries the partner if the breaker allows it.
func (b *Breaker) Status(ctx context.Context, endToEndID string) (application.AuthoritativeStatus, error) {
	if !b.allow() {
		return application.AuthoritativeStatus{}, ErrBreakerOpen
	}

	status, err := b.client.Status(ctx, endToEndID)
	b.record(err == nil)

	return status, err
}

func (b *Breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case breakerClosed:
		return true
	case breakerOpen:
		if b.now().Sub(b.openedAt) < breakerOpenFor {
			return false
		}

		b.state = breakerHalfOpen
		b.isProbeOnAir = true

		return true
	case breakerHalfOpen:
		if b.isProbeOnAir {
			return false
		}

		b.isProbeOnAir = true

		return true
	default:
		return false
	}
}

func (b *Breaker) record(isSuccess bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.state == breakerHalfOpen {
		b.closeOrReopen(isSuccess)

		return
	}

	b.outcomes = append(b.outcomes, isSuccess)
	if len(b.outcomes) > breakerWindow {
		b.outcomes = b.outcomes[len(b.outcomes)-breakerWindow:]
	}

	isTripped := len(b.outcomes) == breakerWindow && failuresIn(b.outcomes)*breakerFailureDen >= breakerWindow*breakerFailureNum
	if isTripped {
		b.open()
	}
}

// closeOrReopen settles the half-open probe.
func (b *Breaker) closeOrReopen(isSuccess bool) {
	b.isProbeOnAir = false

	if !isSuccess {
		b.open()

		return
	}

	b.state = breakerClosed
	b.outcomes = nil
}

func (b *Breaker) open() {
	b.state = breakerOpen
	b.openedAt = b.now()
	b.outcomes = nil
}

func failuresIn(outcomes []bool) int {
	failures := 0

	for _, isSuccess := range outcomes {
		if !isSuccess {
			failures++
		}
	}

	return failures
}
