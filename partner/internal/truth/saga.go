package truth

import "context"

// Saga decides the fate of the transfers the partner holds: settle, reject or
// return, each once, in the order the state machine allows.
type Saga struct {
	db Transactor
}

// NewSaga builds the saga over a transactional store.
func NewSaga(db Transactor) Saga {
	return Saga{db: db}
}

// Settle moves the money out of the participant's reserve: one statement
// entry, or two for a transfer the partner processes twice by mistake.
func (s Saga) Settle(ctx context.Context, command SettleCommand) error {
	return s.db.InTx(ctx, func(ctx context.Context, tx Tx) error {
		transfer, err := tx.FindTransfer(ctx, command.EndToEndID)
		if err != nil {
			return err
		}

		if !transfer.canSettle() {
			return nil
		}

		err = tx.MarkSettled(ctx, transfer.EndToEndID, command.ReturnAt)
		if err != nil {
			return err
		}

		for range transfer.settlements() {
			err = tx.AddEntry(ctx, transfer.entry(EntrySettlement, command.Now))
			if err != nil {
				return err
			}
		}

		return publish(ctx, tx, transfer.event(EventSettled, command.EventID, command.Now), command.Planner)
	})
}

// Reject is a definitive refusal: nothing leaves the reserve.
func (s Saga) Reject(ctx context.Context, command RejectCommand) error {
	return s.db.InTx(ctx, func(ctx context.Context, tx Tx) error {
		transfer, err := tx.FindTransfer(ctx, command.EndToEndID)
		if err != nil {
			return err
		}

		if !transfer.canReject() {
			return nil
		}

		err = tx.MarkRejected(ctx, transfer.EndToEndID, command.Reason)
		if err != nil {
			return err
		}

		event := transfer.event(EventRejected, command.EventID, command.Now)
		event.Reason = command.Reason

		return publish(ctx, tx, event, command.Planner)
	})
}

// Return gives a settled transfer's money back to the reserve.
func (s Saga) Return(ctx context.Context, command ReturnCommand) error {
	return s.db.InTx(ctx, func(ctx context.Context, tx Tx) error {
		transfer, err := tx.FindTransfer(ctx, command.EndToEndID)
		if err != nil {
			return err
		}

		if !transfer.canReturn() {
			return nil
		}

		err = tx.MarkReturned(ctx, transfer.EndToEndID)
		if err != nil {
			return err
		}

		err = tx.AddEntry(ctx, transfer.entry(EntryReturn, command.Now))
		if err != nil {
			return err
		}

		return publish(ctx, tx, transfer.event(EventReturned, command.EventID, command.Now), command.Planner)
	})
}

// publish numbers a transfer's webhook within the transfer and stores the
// deliveries the planner chose; a PIX in has no transfer to number it by.
func publish(ctx context.Context, tx Tx, event Event, planner Planner) error {
	isTransferEvent := event.Type != EventIncoming
	if isTransferEvent {
		seq, err := tx.NextWebhookSeq(ctx, event.EndToEndID)
		if err != nil {
			return err
		}

		event.Sequence = seq
	}

	deliveries, err := planDeliveries(event, planner)
	if err != nil {
		return err
	}

	for _, delivery := range deliveries {
		err = tx.AddDelivery(ctx, delivery)
		if err != nil {
			return err
		}
	}

	return nil
}
