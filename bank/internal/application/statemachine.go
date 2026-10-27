package application

import (
	"cmp"
	"context"
	"fmt"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/transfer"
)

type TransitionCause string

type TransitionCommand struct {
	TransferID transfer.ID
	// From is the status the trigger was meant for: a webhook "settled"
	// speaks for a transfer in sent, the relay's answer for one in created.
	From          transfer.Status
	To            transfer.Status
	FailureCode   string
	Cause         TransitionCause
	CorrelationID string
}

type TransitionStores interface {
	PixStore
	PostingStores
}

type StateMachine interface {
	// The guarded machine refuses, with transfer.ErrInvalidStateTransition, a
	// transfer that is not in From.
	Move(ctx context.Context, stores TransitionStores, command TransitionCommand) error
}

// GuardedTransitions is the state_machine defense: the transfer is locked, its
// current status must be the command's From and the move an allowed transition;
// the status changes by compare-and-set and the ledger gets exactly the posting
// the transition requires.
type GuardedTransitions struct{}

// OverwriteTransitions is the naive variant: it writes the new status whatever
// the current one is and posts what the target implies, without asking where
// the transfer comes from.
type OverwriteTransitions struct{}

type transitionStep struct {
	external ExternalTransfer
	from     transfer.Status
	seq      int
	posting  transfer.Posting
	command  TransitionCommand
}

func (GuardedTransitions) Move(ctx context.Context, stores TransitionStores, command TransitionCommand) error {
	external, err := stores.LockExternalTransfer(ctx, command.TransferID)
	if err != nil {
		return err
	}

	if external.Status != command.From {
		return fmt.Errorf("%w: %s is in %s, the trigger expected %s", transfer.ErrInvalidStateTransition, external.ID, external.Status, command.From)
	}

	posting, err := transfer.PostingOf(command.From, command.To)
	if err != nil {
		return err
	}

	change := StatusChange{TransferID: command.TransferID, To: command.To, FailureCode: command.FailureCode}

	err = stores.AdvanceStatus(ctx, change, external.StatusSeq)
	if err != nil {
		return err
	}

	step := transitionStep{external: external, from: command.From, seq: external.StatusSeq + 1, posting: posting, command: command}

	return recordStep(ctx, stores, step)
}

func (OverwriteTransitions) Move(ctx context.Context, stores TransitionStores, command TransitionCommand) error {
	external, _, err := stores.FindExternalTransfer(ctx, command.TransferID)
	if err != nil {
		return err
	}

	change := StatusChange{TransferID: command.TransferID, To: command.To, FailureCode: command.FailureCode}

	from, seq, err := stores.OverwriteStatus(ctx, change)
	if err != nil {
		return err
	}

	step := transitionStep{external: external, from: from, seq: seq, posting: postingTowards(command.To), command: command}

	return recordStep(ctx, stores, step)
}

func recordStep(ctx context.Context, stores TransitionStores, step transitionStep) error {
	var ledgerTransactionID *ledger.TransactionID

	if step.posting != transfer.PostingNone {
		transactionID, err := postTransferStep(ctx, stores, step.external, step.posting)
		if err != nil {
			return err
		}

		ledgerTransactionID = &transactionID
	}

	correlationID := cmp.Or(step.command.CorrelationID, step.external.ClientRef)
	transition := TransitionRecord{
		TransferID: step.external.ID, Seq: step.seq, From: &step.from, To: step.command.To, Cause: step.command.Cause,
		LedgerTransactionID: ledgerTransactionID, CorrelationID: correlationID,
	}

	return stores.InsertTransition(ctx, transition)
}

func postingTowards(to transfer.Status) transfer.Posting {
	switch to {
	case transfer.StatusConfirmed:
		return transfer.PostingSettle
	case transfer.StatusFailed:
		return transfer.PostingRefund
	case transfer.StatusReversed:
		return transfer.PostingReturn
	case transfer.StatusCreated, transfer.StatusSent:
		return transfer.PostingNone
	default:
		return transfer.PostingNone
	}
}
