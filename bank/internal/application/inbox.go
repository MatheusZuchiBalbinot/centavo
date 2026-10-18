package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/correlation"
	"centavo/bank/internal/platform/failpoint"
)

// Inbox timings.
const (
	InboxPoll       = 500 * time.Millisecond
	inboxRetryDelay = 2 * time.Second
)

// InboxStatus is where an inbox event ended.
type InboxStatus string

// Final statuses of an inbox event.
const (
	InboxProcessed InboxStatus = "processed"
	// InboxConflict is an event the bank cannot apply: it waits for a human or
	// for the reconciliation.
	InboxConflict InboxStatus = "conflict"
)

// Inbox outcomes, kept with the event for the audit.
const (
	outcomeApplied   = "applied"
	outcomeDuplicate = "duplicate"
	// outcomeResynced is an event that did not fit the transfer's status:
	// the processor asked the partner and walked to its answer instead.
	outcomeResynced = "resynced"
)

// errNoInboxEvent ends a processor pass with nothing to do.
var errNoInboxEvent = errors.New("no inbox event ready")

// InboxEvent is a stored partner event waiting to be applied.
type InboxEvent struct {
	ID            int64
	Event         PartnerEvent
	CorrelationID string
}

// InboxStore keeps the inbox.
type InboxStore interface {
	// InsertInbox records the event; false means it was already there.
	InsertInbox(ctx context.Context, event PartnerEvent, correlationID string) (bool, error)
	ClaimInbox(ctx context.Context) (InboxEvent, bool, error)
	// ReclaimInbox locks an event again in a later transaction, if it is
	// still pending and no other processor holds it.
	ReclaimInbox(ctx context.Context, id int64) (bool, error)
	FinishInbox(ctx context.Context, id int64, status InboxStatus, outcome string) error
	DeferInbox(ctx context.Context, id int64, delay time.Duration, reason string) error
}

// InboxProcessor applies inbox events, each in the same transaction that marks
// it, so a crash in between leaves neither.
type InboxProcessor struct {
	unitOfWork UnitOfWork
	events     eventApplier
	machine    StateMachine
	partner    PartnerStatusReader
	logger     *slog.Logger
}

// InboxProcessorConfig wires a processor.
type InboxProcessorConfig struct {
	UnitOfWork UnitOfWork
	Machine    StateMachine
	// Partner answers the status queries of a resync, which only the guarded
	// machine (state_machine) asks for.
	Partner PartnerStatusReader
	Logger  *slog.Logger
}

// NewInboxProcessor builds the processor.
func NewInboxProcessor(config InboxProcessorConfig) InboxProcessor {
	return InboxProcessor{
		unitOfWork: config.UnitOfWork, events: eventApplier{machine: config.Machine}, machine: config.Machine, partner: config.Partner, logger: config.Logger,
	}
}

// Run drains the inbox, then polls it until ctx ends.
func (p InboxProcessor) Run(ctx context.Context) {
	ticker := time.NewTicker(InboxPoll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		p.drain(ctx)
	}
}

func (p InboxProcessor) drain(ctx context.Context) {
	for ctx.Err() == nil {
		hasMore, err := p.RunOnce(ctx)
		if err != nil {
			p.logger.WarnContext(ctx, "processing the inbox", "error", err)

			return
		}

		if !hasMore {
			return
		}
	}
}

// RunOnce applies one event; false means the inbox had none ready.
func (p InboxProcessor) RunOnce(ctx context.Context) (bool, error) {
	var claimed InboxEvent

	err := p.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		event, isFound, err := stores.ClaimInbox(ctx)
		if err != nil {
			return err
		}

		if !isFound {
			return errNoInboxEvent
		}

		claimed = event
		// The event's correlation id follows it into the logs and the
		// failpoint events, so the checker can tell its story.
		ctx = correlation.With(ctx, event.CorrelationID)

		err = p.events.apply(ctx, stores, event.Event)
		if err != nil {
			return err
		}

		// `inbox.after_apply_before_mark`: inside the transaction, so dying
		// here undoes the effect too.
		err = failpoint.Hit(ctx, failpoint.InboxAfterApplyBeforeMark)
		if err != nil {
			return err
		}

		return stores.FinishInbox(ctx, event.ID, InboxProcessed, outcomeApplied)
	})
	if errors.Is(err, errNoInboxEvent) {
		return false, nil
	}

	if err == nil {
		return true, nil
	}

	// A failed apply rolled its transaction back; what to do with the event
	// is decided in a new one.
	return true, p.settleFailure(ctx, claimed, err)
}

func (p InboxProcessor) settleFailure(ctx context.Context, claimed InboxEvent, cause error) error {
	if claimed.ID == 0 {
		return cause
	}

	if errors.Is(cause, transfer.ErrInvalidStateTransition) {
		cause = p.resync(ctx, claimed)
	}

	if cause == nil {
		return nil
	}

	return p.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		if errors.Is(cause, ErrAlreadyCredited) {
			return stores.FinishInbox(ctx, claimed.ID, InboxProcessed, outcomeDuplicate)
		}

		isUnappliable := errors.Is(cause, transfer.ErrTransferNotFound) || errors.Is(cause, ErrUnknownEvent) || isUnresolvable(cause)
		if isUnappliable {
			p.logger.WarnContext(ctx, "inbox event in conflict", "inbox_id", claimed.ID, "event_id", claimed.Event.ID, "error", cause)

			return stores.FinishInbox(ctx, claimed.ID, InboxConflict, cause.Error())
		}

		p.logger.WarnContext(ctx, "deferring inbox event", "inbox_id", claimed.ID, "event_id", claimed.Event.ID, "error", cause)

		return stores.DeferInbox(ctx, claimed.ID, inboxRetryDelay, cause.Error())
	})
}

// resync handles an event that does not fit the transfer's status: out of
// order, repeated under a new id, or contradictory. The partner is asked
// outside any transaction, and the transfer walks to its answer in a new one,
// which also closes the event.
func (p InboxProcessor) resync(ctx context.Context, claimed InboxEvent) error {
	ctx = correlation.With(ctx, claimed.CorrelationID)

	authoritative, err := p.partner.Status(ctx, claimed.Event.EndToEndID)
	if err != nil {
		return fmt.Errorf("%w: %w", errPartnerUnreachable, err)
	}

	return p.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		// The failed apply rolled back and freed the event, so another
		// processor may have it now. Taking the event before the transfer
		// keeps the order every apply uses; one that holds the event and
		// waits on this transfer would otherwise wait on us in a circle.
		isOurs, err := stores.ReclaimInbox(ctx, claimed.ID)
		if err != nil {
			return err
		}

		if !isOurs {
			return nil
		}

		external, err := stores.FindExternalTransferByEndToEnd(ctx, claimed.Event.EndToEndID)
		if err != nil {
			return err
		}

		walk := pathWalk{transferID: external.ID, authoritative: authoritative, cause: causeResync, correlationID: claimed.Event.ID}

		err = walkPath(ctx, stores, p.machine, walk)
		if err != nil {
			return err
		}

		err = failpoint.Hit(ctx, failpoint.InboxAfterApplyBeforeMark)
		if err != nil {
			return err
		}

		return stores.FinishInbox(ctx, claimed.ID, InboxProcessed, outcomeResynced)
	})
}
