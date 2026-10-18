package application

import (
	"cmp"
	"context"

	"centavo/bank/internal/platform/correlation"
)

// InboxIntake is the inbox defense's webhook side: the event is only written
// down, once per event_id, before the 200. The partner redelivering it changes
// nothing.
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
		correlationID := cmp.Or(correlation.From(ctx), event.ID)
		_, err := stores.InsertInbox(ctx, event, correlationID)

		return err
	})
}
