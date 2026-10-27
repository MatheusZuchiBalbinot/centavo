package application

import (
	"context"

	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/failpoint"
)

type Publisher interface {
	Publish(ctx context.Context, id transfer.ID) error
}

type Publication interface {
	WithinCommit(ctx context.Context, stores OutboxStore, external ExternalTransfer, correlationID string) error
	AfterCommit(ctx context.Context, id transfer.ID) error
}

// NotifyAfterCommit is the naive publication: once the transfer is committed, a
// NOTIFY tells the workers. Dying between the two, or having no worker
// listening, leaves the transfer in created forever.
type NotifyAfterCommit struct {
	publisher Publisher
}

func NewNotifyAfterCommit(publisher Publisher) NotifyAfterCommit {
	return NotifyAfterCommit{publisher: publisher}
}

func (NotifyAfterCommit) WithinCommit(context.Context, OutboxStore, ExternalTransfer, string) error {
	return nil
}

func (n NotifyAfterCommit) AfterCommit(ctx context.Context, id transfer.ID) error {
	return n.publisher.Publish(ctx, id)
}

// With the outbox the step does nothing and the failpoint is harmless.
func PublishAfterCommit(ctx context.Context, publication Publication, id transfer.ID) error {
	err := failpoint.Hit(ctx, failpoint.PixOutAfterSaveBeforePublish)
	if err != nil {
		return err
	}

	return publication.AfterCommit(ctx, id)
}
