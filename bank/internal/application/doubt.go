package application

import (
	"context"
	"math/rand/v2"
	"time"
)

// Backoff of the relay towards the partner (docs/architecture.md, section 7).
const (
	relayBackoffBase = 200 * time.Millisecond
	relayBackoffCap  = 30 * time.Second
	// maxBackoffDoublings keeps the shift far from overflowing; the cap is
	// reached long before.
	maxBackoffDoublings = 16
)

// doubtReason is what a deferred message records as its last error.
const doubtReason = "partner answer unknown"

// DoubtPolicy is defense D9's strategy for an answer that may hide a PIX the
// partner processed: a timeout, a 5xx, a broken connection or an open
// circuit breaker. It runs in the commit that would have recorded the answer.
type DoubtPolicy interface {
	OnUnknown(ctx context.Context, stores Stores, claim OutboxClaim, external ExternalTransfer) error
}

// GiveUpOnDoubt is the naive variant: doubt is taken for a failure and the
// amount refunded, though the partner may have paid (FC21, PM-008).
type GiveUpOnDoubt struct {
	machine StateMachine
}

// NewGiveUpOnDoubt builds the naive policy.
func NewGiveUpOnDoubt(machine StateMachine) GiveUpOnDoubt {
	return GiveUpOnDoubt{machine: machine}
}

// OnUnknown closes the message and fails the transfer.
func (g GiveUpOnDoubt) OnUnknown(ctx context.Context, stores Stores, claim OutboxClaim, external ExternalTransfer) error {
	isOurs, err := stores.FinishOutbox(ctx, claim.ID, claim.Token)
	if err != nil {
		return err
	}

	if !isOurs {
		return errLeaseLost
	}

	return ignoreMovedOn(g.machine.Move(ctx, stores, answerCommand(external, PartnerUnknown)))
}

// RetryOnDoubt is D9: a timeout never becomes a failure (BR10). The message
// goes back to the outbox after an exponential backoff with full jitter, and
// the next send, under the same end_to_end_id (D6), gets the partner's answer.
type RetryOnDoubt struct{}

// OnUnknown reschedules the message.
func (RetryOnDoubt) OnUnknown(ctx context.Context, stores Stores, claim OutboxClaim, _ ExternalTransfer) error {
	isOurs, err := stores.DeferOutbox(ctx, claim, fullJitterBackoff(claim.Attempts), doubtReason)
	if err != nil {
		return err
	}

	if !isOurs {
		return errLeaseLost
	}

	return nil
}

// fullJitterBackoff waits a random time between zero and the exponential
// ceiling, so workers coming back from a partner outage do not all return at
// once (docs/architecture.md, section 7).
func fullJitterBackoff(attempts int) time.Duration {
	doublings := min(max(attempts-1, 0), maxBackoffDoublings)
	ceiling := min(relayBackoffBase<<doublings, relayBackoffCap)

	return rand.N(ceiling) //nolint:gosec // G404: jitter spreads retries; it guards no secret.
}
