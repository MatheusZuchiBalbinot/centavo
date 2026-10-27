package truth

import (
	"context"
	"errors"
)

type Intake struct {
	db Transactor
}

func NewIntake(db Transactor) Intake {
	return Intake{db: db}
}

// The API is idempotent by end_to_end_id.
func (i Intake) Submit(ctx context.Context, transfer Transfer) (SubmitResult, error) {
	var result SubmitResult

	err := i.db.InTx(ctx, func(ctx context.Context, tx Tx) error {
		existing, err := tx.FindTransfer(ctx, transfer.EndToEndID)
		if err == nil {
			result = SubmitResult{Transfer: existing}

			return nil
		}

		if !errors.Is(err, ErrNotFound) {
			return err
		}

		err = tx.InsertTransfer(ctx, transfer)
		if err != nil {
			return err
		}

		result = SubmitResult{Transfer: transfer, IsNew: true}

		return nil
	})

	return result, err
}

func (i Intake) AddIncoming(ctx context.Context, command IncomingCommand) (bool, error) {
	isNew := false

	err := i.db.InTx(ctx, func(ctx context.Context, tx Tx) error {
		incoming := command.Incoming

		var err error

		isNew, err = tx.InsertIncoming(ctx, incoming)
		if err != nil {
			return err
		}

		if !isNew {
			return nil
		}

		err = tx.AddEntry(ctx, incoming.entry())
		if err != nil {
			return err
		}

		return publish(ctx, tx, incoming.event(command.EventID), command.Planner)
	})

	return isNew, err
}
