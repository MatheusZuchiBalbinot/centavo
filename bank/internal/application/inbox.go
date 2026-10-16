package application

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"time"

	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/correlation"
	"centavo/bank/internal/platform/failpoint"
)

// Inbox timings of docs/architecture.md, section 6.
const (
	InboxPoll       = 500 * time.Millisecond
	inboxRetryDelay = 2 * time.Second
)

// InboxStatus is where an inbox event ended.
type InboxStatus string

// Final statuses of an inbox event.
const (
	InboxProcessed InboxStatus = "processed"
	// InboxConflict is an event the bank cannot apply: it waits for a human
	// or, from F9 on, for the reconciliation.
	InboxConflict InboxStatus = "conflict"
)

// Inbox outcomes, kept with the event for the audit.
const (
	outcomeApplied   = "applied"
	outcomeDuplicate = "duplicate"
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
	FinishInbox(ctx context.Context, id int64, status InboxStatus, outcome string) error
	DeferInbox(ctx context.Context, id int64, delay time.Duration, reason string) error
}

// InboxIntake is defense D5's webhook side (docs/domain.md, section 13.8):
// the event is only written down, once per event_id, before the 200. The
// partner redelivering it changes nothing (FC06).
type InboxIntake struct {
	unitOfWork UnitOfWork
}

// NewInboxIntake builds the handler's use case.
func NewInboxIntake(unitOfWork UnitOfWork) InboxIntake {
	return InboxIntake{unitOfWork: unitOfWork}
}

// Apply records the event.
func (i InboxIntake) Apply(ctx context.Context, event PartnerEvent) error {
	return i.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		// The partner echoes the PIX's correlation id in the webhook; an
		// event without one is still traceable by its own id.
		_, err := stores.InsertInbox(ctx, event, cmp.Or(correlation.From(ctx), event.ID))

		return err
	})
}

// InboxProcessor applies inbox events, each in the same transaction that
// marks it, so a crash in between leaves neither (FC07).
type InboxProcessor struct {
	unitOfWork UnitOfWork
	logger     *slog.Logger
}

// NewInboxProcessor builds the processor.
func NewInboxProcessor(unitOfWork UnitOfWork, logger *slog.Logger) InboxProcessor {
	return InboxProcessor{unitOfWork: unitOfWork, logger: logger}
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

		err = applyEvent(ctx, stores, event.Event)
		if err != nil {
			return err
		}

		// FP06: inside the transaction, so dying here undoes the effect too.
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

	return p.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		if errors.Is(cause, ErrAlreadyCredited) {
			return stores.FinishInbox(ctx, claimed.ID, InboxProcessed, outcomeDuplicate)
		}

		isUnappliable := errors.Is(cause, transfer.ErrTransferNotFound) || errors.Is(cause, ErrUnknownEvent)
		if isUnappliable {
			p.logger.WarnContext(ctx, "inbox event in conflict", "inbox_id", claimed.ID, "event_id", claimed.Event.ID, "error", cause)

			return stores.FinishInbox(ctx, claimed.ID, InboxConflict, cause.Error())
		}

		p.logger.WarnContext(ctx, "deferring inbox event", "inbox_id", claimed.ID, "event_id", claimed.Event.ID, "error", cause)

		return stores.DeferInbox(ctx, claimed.ID, inboxRetryDelay, cause.Error())
	})
}
