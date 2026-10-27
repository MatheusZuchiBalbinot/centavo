package application

import (
	"context"
	"math/rand/v2"
	"time"
)

const (
	relayBackoffBase = 200 * time.Millisecond
	relayBackoffCap  = 30 * time.Second
	// maxBackoffDoublings keeps the shift far from overflowing; the cap is
	// reached long before.
	maxBackoffDoublings = 16
)

const doubtReason = "partner answer unknown"

type DoubtStores interface {
	OutboxStore
	TransitionStores
}

// DoubtPolicy is the resilient_client defense's strategy for an answer that may
// hide a PIX the partner processed: a timeout, a 5xx, a broken connection or an
// open circuit breaker. It runs in the commit that would have recorded the
// answer.
type DoubtPolicy interface {
	OnUnknown(ctx context.Context, stores DoubtStores, claim OutboxClaim, external ExternalTransfer) error
}

// GiveUpOnDoubt is the naive variant: doubt is taken for a failure and the
// amount refunded, though the partner may have paid.
type GiveUpOnDoubt struct {
	machine StateMachine
}

// RetryOnDoubt is the resilient_client defense: a timeout never becomes a
// failure (a transfer fails only on the partner's authoritative refusal). The
// message goes back to the outbox after an exponential backoff with full
// jitter, and the next send, under the same end_to_end_id
// (partner_idempotency), gets the partner's answer.
type RetryOnDoubt struct{}

func NewGiveUpOnDoubt(machine StateMachine) GiveUpOnDoubt {
	return GiveUpOnDoubt{machine: machine}
}

func (g GiveUpOnDoubt) OnUnknown(ctx context.Context, stores DoubtStores, claim OutboxClaim, external ExternalTransfer) error {
	isOurs, err := stores.FinishOutbox(ctx, claim.ID, claim.Token)
	if err != nil {
		return err
	}

	if !isOurs {
		return errLeaseLost
	}

	command := answerCommand(external, PartnerUnknown)

	return ignoreMovedOn(g.machine.Move(ctx, stores, command))
}

func (RetryOnDoubt) OnUnknown(ctx context.Context, stores DoubtStores, claim OutboxClaim, _ ExternalTransfer) error {
	isOurs, err := stores.DeferOutbox(ctx, claim, fullJitterBackoff(claim.Attempts), doubtReason)
	if err != nil {
		return err
	}

	if !isOurs {
		return errLeaseLost
	}

	return nil
}

// So workers coming back from a partner outage do not all return at once.
func fullJitterBackoff(attempts int) time.Duration {
	doublings := min(max(attempts-1, 0), maxBackoffDoublings)
	ceiling := min(relayBackoffBase<<doublings, relayBackoffCap)

	return rand.N(ceiling) //nolint:gosec // G404: jitter spreads retries; it guards no secret.
}
