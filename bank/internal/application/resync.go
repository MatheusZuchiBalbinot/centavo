package application

import (
	"cmp"
	"context"
	"errors"

	"centavo/bank/internal/domain/transfer"
)

// Causes of the transitions a resync records.
const (
	causeResync  = "resync"
	causeSweeper = "sweeper"
)

// errPartnerUnreachable is a status query that got no answer.
var errPartnerUnreachable = errors.New("partner status unreachable")

// AuthoritativeStatus is the partner's answer to a status query: what no
// webhook can override.
type AuthoritativeStatus struct {
	Status transfer.PartnerStatus
	Reason string
}

// PartnerStatusReader queries a PIX's authoritative status.
type PartnerStatusReader interface {
	Status(ctx context.Context, endToEndID transfer.EndToEndID) (AuthoritativeStatus, error)
}

// pathWalk is one resync: a transfer walked to an authoritative status.
type pathWalk struct {
	transferID    transfer.ID
	authoritative AuthoritativeStatus
	cause         string
	correlationID string
}

// walkPath applies, in the caller's unit of work, the shortest valid path
// from the transfer's current status to the partner's (docs/domain.md,
// section 8.4), each step a guarded transition with its own posting. The
// current status is read under the lock, so a concurrent walk finds nothing
// left to do.
func walkPath(ctx context.Context, stores Stores, machine StateMachine, walk pathWalk) error {
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

// isUnresolvable reports a disagreement a resync must not settle by itself.
func isUnresolvable(err error) bool {
	return errors.Is(err, transfer.ErrStatusConflict) || errors.Is(err, transfer.ErrSentButUnknown)
}
