package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/correlation"
	"centavo/bank/internal/platform/failpoint"
)

// TopicPixOutSubmit is the outbox topic of a PIX out to send.
const TopicPixOutSubmit = "pix_out.submit"

// Outbox timings of docs/architecture.md, section 6.
const (
	OutboxLeaseTTL   = 30 * time.Second
	OutboxPoll       = 500 * time.Millisecond
	outboxClaimBatch = 10
)

// errLeaseLost means another relay took the message: this one is a zombie
// and must throw its result away.
var errLeaseLost = errors.New("outbox lease lost to another relay")

// OutboxMessage is a message to deliver after the commit that wrote it.
type OutboxMessage struct {
	Topic         string
	AggregateID   uuid.UUID
	Payload       []byte
	CorrelationID string
}

// OutboxClaim is a message a relay leased: only its token may finish it.
type OutboxClaim struct {
	ID            int64
	AggregateID   uuid.UUID
	Token         int64
	Attempts      int
	CorrelationID string
}

// OutboxStore keeps the outbox.
type OutboxStore interface {
	EnqueueOutbox(ctx context.Context, message OutboxMessage) error
	// ClaimOutbox leases up to limit ready messages with a fresh token each.
	ClaimOutbox(ctx context.Context, owner string, ttl time.Duration, limit int) ([]OutboxClaim, error)
	// FinishOutbox marks the message delivered if the token still owns it.
	FinishOutbox(ctx context.Context, id, token int64) (bool, error)
	// DeferOutbox releases the message for another attempt after delay, if
	// the token still owns it.
	DeferOutbox(ctx context.Context, claim OutboxClaim, delay time.Duration, reason string) (bool, error)
}

// OutboxPublication is defense D4 (docs/domain.md, section 13.7): the
// message to send is written in the same commit as the transfer, so it can
// never be lost between the two (FC04).
type OutboxPublication struct{}

type pixOutPayload struct {
	TransferID string `json:"transfer_id"`
}

// WithinCommit writes the outbox message.
func (OutboxPublication) WithinCommit(ctx context.Context, stores Stores, external ExternalTransfer, correlationID string) error {
	payload, err := json.Marshal(pixOutPayload{TransferID: external.ID.String()})
	if err != nil {
		return err
	}

	message := OutboxMessage{Topic: TopicPixOutSubmit, AggregateID: uuid.UUID(external.ID), Payload: payload, CorrelationID: correlationID}

	return stores.EnqueueOutbox(ctx, message)
}

// AfterCommit does nothing: the relay polls the table.
func (OutboxPublication) AfterCommit(context.Context, transfer.ID) error {
	return nil
}

// EndToEndPolicy is defense D6's strategy: which end_to_end_id a send uses.
type EndToEndPolicy interface {
	Next(external ExternalTransfer) string
}

// FixedEndToEnd is D6: the id derived at creation, on every send, so the
// partner sees a retry as the same PIX and never pays twice (BR11).
type FixedEndToEnd struct{}

// Next returns the transfer's own id.
func (FixedEndToEnd) Next(external ExternalTransfer) string {
	return external.ID.EndToEndID()
}

// NewEndToEndPerAttempt is the naive variant: E<id>-<attempt>.
type NewEndToEndPerAttempt struct{}

// Next returns the id of the following attempt.
func (NewEndToEndPerAttempt) Next(external ExternalTransfer) string {
	return external.ID.AttemptEndToEndID(nextAttemptOf(external.EndToEndID))
}

// OutboxRelay delivers outbox messages to the partner (docs/architecture.md,
// section 5.3), with a lease per message and the token as fencing.
type OutboxRelay struct {
	unitOfWork UnitOfWork
	partner    PartnerSubmitter
	endToEnd   EndToEndPolicy
	machine    StateMachine
	doubt      DoubtPolicy
	owner      string
	leaseTTL   time.Duration
	logger     *slog.Logger
}

// OutboxRelayConfig wires a relay.
type OutboxRelayConfig struct {
	UnitOfWork UnitOfWork
	Partner    PartnerSubmitter
	EndToEnd   EndToEndPolicy
	Machine    StateMachine
	Doubt      DoubtPolicy
	// Owner names this relay in the leases it takes.
	Owner string
	// LeaseTTL is how long a message stays with this relay before another
	// may take it; OutboxLeaseTTL in production.
	LeaseTTL time.Duration
	Logger   *slog.Logger
}

// NewOutboxRelay builds the relay.
func NewOutboxRelay(config OutboxRelayConfig) OutboxRelay {
	return OutboxRelay{unitOfWork: config.UnitOfWork, partner: config.Partner, endToEnd: config.EndToEnd, machine: config.Machine, doubt: config.Doubt, owner: config.Owner, leaseTTL: config.LeaseTTL, logger: config.Logger}
}

// Run polls the outbox until ctx ends. Polling is the source of truth; a
// NOTIFY would only make it sooner (ADR-003).
func (r OutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(OutboxPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		err := r.RunOnce(ctx)
		if err != nil {
			r.logger.WarnContext(ctx, "relaying the outbox", "error", err)
		}
	}
}

// RunOnce leases one batch and delivers it.
func (r OutboxRelay) RunOnce(ctx context.Context) error {
	var claims []OutboxClaim

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		claims, err = stores.ClaimOutbox(ctx, r.owner, r.leaseTTL, outboxClaimBatch)

		return err
	})
	if err != nil {
		return err
	}

	for _, claim := range claims {
		err := r.deliver(ctx, claim)
		if errors.Is(err, errLeaseLost) {
			r.logger.InfoContext(ctx, "discarding a result: the lease moved on", "message_id", claim.ID, "token", claim.Token)

			continue
		}

		if err != nil {
			return err
		}
	}

	return nil
}

func (r OutboxRelay) deliver(ctx context.Context, claim OutboxClaim) error {
	ctx = correlation.With(ctx, claim.CorrelationID)

	external, err := r.load(ctx, transfer.ID(claim.AggregateID))
	if err != nil {
		return err
	}

	if external.Status != transfer.StatusCreated {
		return r.finish(ctx, claim, nil)
	}

	external, err = r.useEndToEnd(ctx, external)
	if err != nil {
		return err
	}

	submission := PartnerSubmission{EndToEndID: external.EndToEndID, AmountCents: external.Amount.Cents(), DestinationKey: external.DestinationKey, CorrelationID: claim.CorrelationID}
	answer := r.partner.Submit(ctx, submission)

	// FP03: with the outbox the message stays pending; the lease expires and
	// another relay sends again, under the same end_to_end_id (D6).
	err = failpoint.Hit(ctx, failpoint.RelayAfterPartnerCallBeforeSave)
	if err != nil {
		return err
	}

	if answer == PartnerUnknown {
		return r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
			return r.doubt.OnUnknown(ctx, stores, claim, external)
		})
	}

	command := answerCommand(external, answer)

	return r.finish(ctx, claim, &command)
}

// useEndToEnd switches the transfer to the id this send uses; with D6 it
// never changes.
func (r OutboxRelay) useEndToEnd(ctx context.Context, external ExternalTransfer) (ExternalTransfer, error) {
	next := r.endToEnd.Next(external)
	if next == external.EndToEndID {
		return external, nil
	}

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		_, err := stores.ClaimAttempt(ctx, external.ID, external.EndToEndID, next)

		return err
	})

	external.EndToEndID = next

	return external, err
}

// finish closes the message and applies the partner's answer in one commit;
// a stale token aborts both.
func (r OutboxRelay) finish(ctx context.Context, claim OutboxClaim, command *TransitionCommand) error {
	return r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		isOurs, err := stores.FinishOutbox(ctx, claim.ID, claim.Token)
		if err != nil {
			return err
		}

		if !isOurs {
			return errLeaseLost
		}

		if command == nil {
			return nil
		}

		return ignoreMovedOn(r.machine.Move(ctx, stores, *command))
	})
}

func (r OutboxRelay) load(ctx context.Context, id transfer.ID) (ExternalTransfer, error) {
	var external ExternalTransfer

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		external, _, err = stores.FindExternalTransfer(ctx, id)

		return err
	})

	return external, err
}

// answerCommand is what the partner's answer does to a transfer in created.
// Only the naive relay gets here with a doubtful answer, and takes it for a
// failure (FC21).
func answerCommand(external ExternalTransfer, answer PartnerAnswer) TransitionCommand {
	command := TransitionCommand{TransferID: external.ID, From: transfer.StatusCreated, Cause: causeRelay}

	switch answer {
	case PartnerAccepted:
		command.To = transfer.StatusSent
	case PartnerRejected:
		command.To = transfer.StatusFailed
		command.FailureCode = failurePartner
	case PartnerUnknown:
		command.To = transfer.StatusFailed
		command.FailureCode = failureTimeout
	}

	return command
}

// ignoreMovedOn drops the guarded machine's refusal: the transfer moved on
// while the partner was answering, and that answer no longer speaks for it.
func ignoreMovedOn(err error) error {
	if errors.Is(err, transfer.ErrInvalidStateTransition) {
		return nil
	}

	return err
}
