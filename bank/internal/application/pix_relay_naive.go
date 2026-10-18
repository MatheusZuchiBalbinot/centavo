package application

import (
	"context"

	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/correlation"
	"centavo/bank/internal/platform/failpoint"
)

// NaiveRelay sends transfers to the partner.
type NaiveRelay struct {
	unitOfWork UnitOfWork
	partner    PartnerSubmitter
	machine    StateMachine
}

// NewNaiveRelay builds the relay.
func NewNaiveRelay(unitOfWork UnitOfWork, partner PartnerSubmitter, machine StateMachine) NaiveRelay {
	return NaiveRelay{unitOfWork: unitOfWork, partner: partner, machine: machine}
}

// Relay sends one transfer. Each attempt gets a new end_to_end_id, so a retry
// the partner already processed is paid twice; a timeout is taken as a failure
// and refunded, though the partner may have paid.
func (r NaiveRelay) Relay(ctx context.Context, id transfer.ID) error {
	var external ExternalTransfer

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		external, _, err = stores.FindExternalTransfer(ctx, id)

		return err
	})
	if err != nil {
		return err
	}

	if external.Status != transfer.StatusCreated {
		return nil
	}

	// The transfer's client_ref follows it into the logs and the failpoint
	// events, as the request's correlation id did in the API.
	ctx = correlation.With(ctx, external.ClientRef)

	attempt := nextAttemptOf(external.EndToEndID)
	attemptID := id.AttemptEndToEndID(attempt)

	isClaimed, err := r.claim(ctx, id, external.EndToEndID, attemptID)
	if err != nil {
		return err
	}

	if !isClaimed {
		return nil
	}

	submission := PartnerSubmission{
		EndToEndID: attemptID, AmountCents: external.Amount.Cents(), DestinationKey: external.DestinationKey, CorrelationID: external.ClientRef,
	}
	answer := r.partner.Submit(ctx, submission)

	return r.record(ctx, external, answer)
}

// RelayPending is the naive recovery: when a worker starts, it sends again
// every transfer still in created, with no lease. A transfer the dead worker
// had already handed to the partner goes out a second time, under a new
// end_to_end_id.
func (r NaiveRelay) RelayPending(ctx context.Context) error {
	var pending []transfer.ID

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		pending, err = stores.ListCreated(ctx, pendingOnBoot)

		return err
	})
	if err != nil {
		return err
	}

	for _, id := range pending {
		err := r.Relay(ctx, id)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r NaiveRelay) claim(ctx context.Context, id transfer.ID, seen, next transfer.EndToEndID) (bool, error) {
	var isClaimed bool

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		isClaimed, err = stores.ClaimAttempt(ctx, id, seen, next)

		return err
	})

	return isClaimed, err
}

func (r NaiveRelay) record(ctx context.Context, external ExternalTransfer, answer PartnerAnswer) error {
	if answer == PartnerAccepted {
		// `relay.after_partner_call_before_save`: the partner took the PIX;
		// dying here leaves it created and a later attempt sends it again under
		// a new id.
		err := failpoint.Hit(ctx, failpoint.RelayAfterPartnerCallBeforeSave)
		if err != nil {
			return err
		}
	}

	command := answerCommand(external, answer)

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		return r.machine.Move(ctx, stores, command)
	})

	return ignoreMovedOn(err)
}
