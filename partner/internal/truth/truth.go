// Package truth is the partner's own record of what it did. The partner may
// lie in answers and deliveries, never here. Double processing is the
// exception: it is the partner's own bug, so it changes the truth.
package truth

import (
	"errors"
	"time"
)

const (
	StatusProcessing Status = "processing"
	StatusSettled    Status = "settled"
	StatusRejected   Status = "rejected"
	StatusReturned   Status = "returned"
)

const (
	EntrySettlement EntryKind = "settlement"
	EntryReturn     EntryKind = "return"
	EntryIncoming   EntryKind = "incoming"
)

const (
	EventSettled  EventType = "transfer.settled"
	EventRejected EventType = "transfer.rejected"
	EventReturned EventType = "transfer.returned"
	EventIncoming EventType = "incoming.received"
)

const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliveryDelivered DeliveryStatus = "delivered"
	// DeliveryAbandoned is a provider giving up after its retries, or a dropped webhook.
	DeliveryAbandoned DeliveryStatus = "abandoned"
)

var ErrNotFound = errors.New("not found")

type Status string

type EntryKind string

type EventType string

type DeliveryStatus string

type Transfer struct {
	EndToEndID        string
	ParticipantID     string
	AmountCents       int64
	DestinationKey    string
	Status            Status
	RejectReason      string
	IsDoubleProcessed bool
	SettleAt          time.Time
	ReturnAt          *time.Time
	CreatedAt         time.Time
	// Echoed on every webhook of the transfer.
	CorrelationID string
}

type StatementEntry struct {
	Seq                  int64     `json:"seq"`
	ParticipantID        string    `json:"participant_id"`
	Kind                 EntryKind `json:"kind"`
	EndToEndID           string    `json:"end_to_end_id"`
	AmountCents          int64     `json:"amount_cents"`
	CreatedAt            time.Time `json:"created_at"`
	DestinationAccountID string    `json:"destination_account_id,omitempty"`
	// So the participant's reconciliation can follow the PIX in its logs and
	// failpoints.
	CorrelationID string `json:"correlation_id,omitempty"`
}

type Incoming struct {
	EndToEndID    string
	ParticipantID string
	AccountID     string
	AmountCents   int64
	CreatedAt     time.Time
	CorrelationID string
}

type Event struct {
	ID            string    `json:"event_id"`
	Type          EventType `json:"type"`
	EndToEndID    string    `json:"end_to_end_id"`
	AmountCents   int64     `json:"amount_cents"`
	Sequence      int       `json:"sequence"`
	OccurredAt    time.Time `json:"occurred_at"`
	Reason        string    `json:"reason,omitempty"`
	AccountID     string    `json:"destination_account_id,omitempty"`
	ParticipantID string    `json:"-"`
	CorrelationID string    `json:"-"`
}

type Delivery struct {
	ID            int64
	EventID       string
	ParticipantID string
	Payload       []byte
	Attempts      int
	NextAttemptAt time.Time
	Status        DeliveryStatus
	CorrelationID string
}

type PlannedDelivery struct {
	EventID   string
	At        time.Time
	IsDropped bool
}

// Planner decides how an event is delivered: once and now when honest, or
// late, twice, with a new id or never, when the partner lies.
type Planner func(event Event, now time.Time) []PlannedDelivery

type SubmitResult struct {
	Transfer Transfer
	IsNew    bool
}

type SettleCommand struct {
	EndToEndID string
	Now        time.Time
	ReturnAt   *time.Time
	EventID    string
	Planner    Planner
}

type RejectCommand struct {
	EndToEndID string
	Reason     string
	Now        time.Time
	EventID    string
	Planner    Planner
}

type ReturnCommand struct {
	EndToEndID string
	Now        time.Time
	EventID    string
	Planner    Planner
}

type IncomingCommand struct {
	Incoming Incoming
	EventID  string
	Planner  Planner
}

type TransferView struct {
	EndToEndID        string    `json:"end_to_end_id"`
	ParticipantID     string    `json:"participant_id"`
	AmountCents       int64     `json:"amount_cents"`
	DestinationKey    string    `json:"destination_key"`
	Status            Status    `json:"status"`
	RejectReason      string    `json:"reject_reason,omitempty"`
	IsDoubleProcessed bool      `json:"double_processed"`
	CreatedAt         time.Time `json:"created_at"`
}

type IncomingView struct {
	EndToEndID    string    `json:"end_to_end_id"`
	ParticipantID string    `json:"participant_id"`
	AccountID     string    `json:"account_id"`
	AmountCents   int64     `json:"amount_cents"`
	CreatedAt     time.Time `json:"created_at"`
}

// Double processing included: what the checker compares the bank against.
type Export struct {
	ParticipantID     string           `json:"participant_id"`
	Transfers         []TransferView   `json:"transfers"`
	Statement         []StatementEntry `json:"statement"`
	Incoming          []IncomingView   `json:"incoming"`
	PendingDeliveries int              `json:"pending_deliveries"`
}
