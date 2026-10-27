package application

import (
	"cmp"
	"context"
	"errors"

	"centavo/bank/internal/domain/transfer"
)

const (
	causeResync  TransitionCause = "resync"
	causeSweeper TransitionCause = "sweeper"
)

var errPartnerUnreachable = errors.New("partner status unreachable")

// What no webhook can override.
type AuthoritativeStatus struct {
	Status transfer.PartnerStatus
	Reason string
}

type PartnerStatusReader interface {
	Status(ctx context.Context, endToEndID transfer.EndToEndID) (AuthoritativeStatus, error)
}

type pathWalk struct {
	transferID    transfer.ID
	authoritative AuthoritativeStatus
	cause         TransitionCause
	correlationID string
}

// The shortest valid path from the transfer's status to the partner's, each
// step a guarded transition with its own posting. The status is read under
// the lock, so a concurrent walk finds nothing left to do.
func walkPath(ctx context.Context, stores TransitionStores, machine StateMachine, walk pathWalk) error {
	external, err := stores.LockExternalTransfer(ctx, walk.transferID)
	if err != nil {
		return err
	}

	path, err := transfer.PathTo(external.Status, walk.authoritative.Status)
	if err != nil {
		return err
	}

	from := external.Status

	for _, to := range path {
		command := TransitionCommand{TransferID: walk.transferID, From: from, To: to, Cause: walk.cause, CorrelationID: walk.correlationID}

		if to == transfer.StatusFailed {
			command.FailureCode = cmp.Or(walk.authoritative.Reason, failurePartner)
		}

		err := machine.Move(ctx, stores, command)
		if err != nil {
			return err
		}

		from = to
	}

	return nil
}

func isUnresolvable(err error) bool {
	return errors.Is(err, transfer.ErrStatusConflict) || errors.Is(err, transfer.ErrSentButUnknown)
}
