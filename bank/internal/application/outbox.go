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
	"centavo/bank/internal/platform/labevent"
)

const TopicPixOutSubmit = "pix_out.submit"

const (
	OutboxLeaseTTL   = 30 * time.Second
	OutboxPoll       = 500 * time.Millisecond
	outboxClaimBatch = 10
)

// errLeaseLost means another relay took the message: this one is a zombie
// and must throw its result away.
var errLeaseLost = errors.New("outbox lease lost to another relay")

type OutboxMessage struct {
	Topic         string
	AggregateID   uuid.UUID
	Payload       []byte
	CorrelationID string
}

// Only its token may finish it.
type OutboxClaim struct {
	ID            int64
	AggregateID   uuid.UUID
	Token         int64
	Attempts      int
	CorrelationID string
}

type OutboxStore interface {
	EnqueueOutbox(ctx context.Context, message OutboxMessage) error
	ClaimOutbox(ctx context.Context, owner string, ttl time.Duration, limit int) ([]OutboxClaim, error)
	FinishOutbox(ctx context.Context, id, token int64) (bool, error)
	DeferOutbox(ctx context.Context, claim OutboxClaim, delay time.Duration, reason string) (bool, error)
}

// OutboxPublication is the outbox defense: the message to send is written in
// the same commit as the transfer, so it can never be lost between the two.
type OutboxPublication struct{}

type EndToEndPolicy interface {
	Next(external ExternalTransfer) transfer.EndToEndID
}

// The partner_idempotency defense: the id derived at creation on every
// send, so the partner sees a retry as the same PIX and never pays twice.
type FixedEndToEnd struct{}

type NewEndToEndPerAttempt struct{}

// A lease per message, with its token as fencing.
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

type OutboxRelayConfig struct {
	UnitOfWork UnitOfWork
	Partner    PartnerSubmitter
	EndToEnd   EndToEndPolicy
	Machine    StateMachine
	Doubt      DoubtPolicy
	Owner      string
	LeaseTTL   time.Duration
	Logger     *slog.Logger
}

type pixOutPayload struct {
	TransferID string `json:"transfer_id"`
}

func (OutboxPublication) WithinCommit(ctx context.Context, stores OutboxStore, external ExternalTransfer, correlationID string) error {
	payload, err := json.Marshal(pixOutPayload{TransferID: external.ID.String()})
	if err != nil {
		return err
	}

	message := OutboxMessage{Topic: TopicPixOutSubmit, AggregateID: uuid.UUID(external.ID), Payload: payload, CorrelationID: correlationID}

	return stores.EnqueueOutbox(ctx, message)
}

// Nothing after the commit: the relay polls the table.
func (OutboxPublication) AfterCommit(context.Context, transfer.ID) error {
	return nil
}

func (FixedEndToEnd) Next(external ExternalTransfer) transfer.EndToEndID {
	return external.ID.EndToEndID()
}

func (NewEndToEndPerAttempt) Next(external ExternalTransfer) transfer.EndToEndID {
	return external.ID.AttemptEndToEndID(nextAttemptOf(external.EndToEndID))
}

func NewOutboxRelay(config OutboxRelayConfig) OutboxRelay {
	return OutboxRelay{
		unitOfWork: config.UnitOfWork, partner: config.Partner, endToEnd: config.EndToEnd, machine: config.Machine,
		doubt: config.Doubt, owner: config.Owner, leaseTTL: config.LeaseTTL, logger: config.Logger,
	}
}

// Polling is the source of truth; a NOTIFY would only make it sooner.
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

	resumed := countResumed(claims)
	if resumed > 0 {
		labevent.Record(ctx, labevent.OutboxResumed, TopicPixOutSubmit, resumed)
	}

	for _, claim := range claims {
		err := r.deliver(ctx, claim)
		if errors.Is(err, errLeaseLost) {
			r.logger.InfoContext(ctx, "discarding a result: the lease moved on", "message_id", claim.ID, "token", claim.Token)
			labevent.Record(ctx, labevent.OutboxFenced, TopicPixOutSubmit, 1)

			continue
		}

		if err != nil {
			return err
		}
	}

	return nil
}

// Messages that already had an attempt: a relay that died or was refused
// before, picked up again.
func countResumed(claims []OutboxClaim) int {
	resumed := 0

	for _, claim := range claims {
		if claim.Attempts > 1 {
			resumed++
		}
	}

	return resumed
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

	submission := PartnerSubmission{
		EndToEndID: external.EndToEndID, AmountCents: external.Amount.Cents(), DestinationKey: external.DestinationKey, CorrelationID: claim.CorrelationID,
	}
	answer := r.partner.Submit(ctx, submission)

	// `relay.after_partner_call_before_save`: with the outbox the message stays
	// pending; the lease expires and another relay sends again, under the same
	// end_to_end_id (partner_idempotency).
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

// With partner_idempotency the id never changes.
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

// A stale token aborts both the close and the partner's answer.
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

// Only the naive relay gets here with a doubtful answer, and takes it for a
// failure.
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

// The transfer moved on while the partner was answering, and that answer no
// longer speaks for it.
func ignoreMovedOn(err error) error {
	if errors.Is(err, transfer.ErrInvalidStateTransition) {
		return nil
	}

	return err
}
