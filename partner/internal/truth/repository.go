package truth

import (
	"context"
	"time"
)

type Transactor interface {
	InTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}

type Tx interface {
	FindTransfer(ctx context.Context, endToEndID string) (Transfer, error)
	InsertTransfer(ctx context.Context, transfer Transfer) error
	MarkSettled(ctx context.Context, endToEndID string, returnAt *time.Time) error
	MarkRejected(ctx context.Context, endToEndID, reason string) error
	MarkReturned(ctx context.Context, endToEndID string) error
	InsertIncoming(ctx context.Context, incoming Incoming) (bool, error)
	AddEntry(ctx context.Context, entry StatementEntry) error
	NextWebhookSeq(ctx context.Context, endToEndID string) (int, error)
	AddDelivery(ctx context.Context, delivery DeliveryRecord) error
}

type DeliveryRecord struct {
	EventID       string
	ParticipantID string
	Payload       []byte
	At            time.Time
	Status        DeliveryStatus
	CorrelationID string
}
