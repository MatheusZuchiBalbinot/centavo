package application

import (
	"context"

	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/failpoint"
)

// Publisher wakes a relay for a new transfer.
type Publisher interface {
	Publish(ctx context.Context, id transfer.ID) error
}

// Publication is the outbox defense's strategy: how a new transfer reaches a
// relay.
type Publication interface {
	// WithinCommit runs in the unit of work that creates the transfer.
	WithinCommit(ctx context.Context, stores OutboxStore, external ExternalTransfer, correlationID string) error
	// AfterCommit runs once the transfer is committed.
	AfterCommit(ctx context.Context, id transfer.ID) error
}

// NotifyAfterCommit is the naive publication: once the transfer is committed, a
// NOTIFY tells the workers. Dying between the two, or having no worker
// listening, leaves the transfer in created forever.
type NotifyAfterCommit struct {
	publisher Publisher
}

// NewNotifyAfterCommit builds the naive publication.
func NewNotifyAfterCommit(publisher Publisher) NotifyAfterCommit {
	return NotifyAfterCommit{publisher: publisher}
}

// WithinCommit does nothing: the naive bank publishes after the commit.
func (NotifyAfterCommit) WithinCommit(context.Context, OutboxStore, ExternalTransfer, string) error {
	return nil
}

// AfterCommit sends the NOTIFY.
func (n NotifyAfterCommit) AfterCommit(ctx context.Context, id transfer.ID) error {
	return n.publisher.Publish(ctx, id)
}

// PublishAfterCommit runs the step after the commit, with
// `pixout.after_save_before_publish` before it. With the outbox the step does
// nothing and the failpoint is harmless.
func PublishAfterCommit(ctx context.Context, publication Publication, id transfer.ID) error {
	err := failpoint.Hit(ctx, failpoint.PixOutAfterSaveBeforePublish)
	if err != nil {
		return err
	}

	return publication.AfterCommit(ctx, id)
}
