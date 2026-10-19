package truth

import (
	"context"
	"time"
)

// Transactor runs fn with a Tx whose writes commit together or not at all.
type Transactor interface {
	InTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

// Tx is everything a use case writes, inside one transaction.
type Tx interface {
	FindTransfer(ctx context.Context, endToEndID string) (Transfer, error)
	InsertTransfer(ctx context.Context, transfer Transfer) error
	MarkSettled(ctx context.Context, endToEndID string, returnAt *time.Time) error
	MarkRejected(ctx context.Context, endToEndID, reason string) error
	MarkReturned(ctx context.Context, endToEndID string) error
	// InsertIncoming records the PIX in and reports whether it was new.
	InsertIncoming(ctx context.Context, incoming Incoming) (bool, error)
	AddEntry(ctx context.Context, entry StatementEntry) error
	// NextWebhookSeq numbers the next webhook of a transfer.
	NextWebhookSeq(ctx context.Context, endToEndID string) (int, error)
	AddDelivery(ctx context.Context, delivery DeliveryRecord) error
}

// DeliveryRecord is one planned webhook delivery, ready to be stored.
type DeliveryRecord struct {
	EventID       string
	ParticipantID string
	Payload       []byte
	At            time.Time
	Status        DeliveryStatus
	CorrelationID string
}
