// Package truth is the partner's own record of what it did: the transfers it
// received, the statement it keeps for each participant and the webhooks it
// owes them. The partner may lie in answers and deliveries; it never lies
// here. Double processing is the one exception: it is a bug of the partner
// itself, so it changes the truth.
//
// The package holds the rules and the use cases; storage keeps them in
// SQLite behind the interfaces of repository.go.
package truth

import (
	"errors"
	"time"
)

// The statuses of a transfer at the partner.
const (
	StatusProcessing Status = "processing"
	StatusSettled    Status = "settled"
	StatusRejected   Status = "rejected"
	StatusReturned   Status = "returned"
)

// The kinds of statement line.
const (
	EntrySettlement EntryKind = "settlement"
	EntryReturn     EntryKind = "return"
	EntryIncoming   EntryKind = "incoming"
)

// The webhook event types.
const (
	EventSettled  EventType = "transfer.settled"
	EventRejected EventType = "transfer.rejected"
	EventReturned EventType = "transfer.returned"
	EventIncoming EventType = "incoming.received"
)

// The states of a webhook delivery.
const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliveryDelivered DeliveryStatus = "delivered"
	// DeliveryAbandoned is a provider giving up after its retries, or a dropped webhook.
	DeliveryAbandoned DeliveryStatus = "abandoned"
)

// ErrNotFound means the partner has no record of the transfer.
var ErrNotFound = errors.New("not found")

// Status is the authoritative status of a transfer.
type Status string

// EntryKind is a line of the participant's statement.
type EntryKind string

// EventType is the type of a webhook.
type EventType string

// DeliveryStatus is where a webhook delivery stands.
type DeliveryStatus string

// Transfer is a PIX the partner received.
type Transfer struct {
	EndToEndID     string
	ParticipantID  string
	AmountCents    int64
	DestinationKey string
	Status         Status
	RejectReason   string
	// IsDoubleProcessed marks a transfer the partner will settle twice.
	IsDoubleProcessed bool
	SettleAt          time.Time
	ReturnAt          *time.Time
	CreatedAt         time.Time
	// CorrelationID is the submitter's, echoed on every webhook of the transfer.
	CorrelationID string
}

// StatementEntry is a line of a participant's statement, ordered by Seq.
type StatementEntry struct {
	Seq           int64     `json:"seq"`
	ParticipantID string    `json:"participant_id"`
	Kind          EntryKind `json:"kind"`
	EndToEndID    string    `json:"end_to_end_id"`
	AmountCents   int64     `json:"amount_cents"`
	CreatedAt     time.Time `json:"created_at"`
	// DestinationAccountID is the account an incoming entry credits.
	DestinationAccountID string `json:"destination_account_id,omitempty"`
	// CorrelationID is the one the PIX came with, so the participant's
	// reconciliation can follow the PIX in its logs and failpoints.
	CorrelationID string `json:"correlation_id,omitempty"`
}

// Incoming is a PIX the partner credited to a participant.
type Incoming struct {
	EndToEndID    string
	ParticipantID string
	AccountID     string
	AmountCents   int64
	CreatedAt     time.Time
	// CorrelationID is the sender's, echoed on the webhook.
	CorrelationID string
}

// Event is one webhook the partner must deliver.
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

// Delivery is one attempt plan for an event; lies can add copies.
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

// PlannedDelivery is a copy of an event to send at a given time.
type PlannedDelivery struct {
	EventID string
	At      time.Time
	// IsDropped records the delivery as abandoned without sending it.
	IsDropped bool
}

// Planner decides how an event is delivered: once and now when honest, or
// late, twice, with a new id or never, when the partner lies.
type Planner func(event Event, now time.Time) []PlannedDelivery

// SubmitResult is what POST /v1/transfers found or created.
type SubmitResult struct {
	Transfer Transfer
	IsNew    bool
}

// SettleCommand settles a transfer.
type SettleCommand struct {
	EndToEndID string
	Now        time.Time
	// ReturnAt schedules a return after the settlement, when the partner lies.
	ReturnAt *time.Time
	EventID  string
	Planner  Planner
}

// RejectCommand rejects a transfer that is still processing.
type RejectCommand struct {
	EndToEndID string
	Reason     string
	Now        time.Time
	EventID    string
	Planner    Planner
}

// ReturnCommand returns a settled transfer.
type ReturnCommand struct {
	EndToEndID string
	Now        time.Time
	EventID    string
	Planner    Planner
}

// IncomingCommand credits an incoming PIX to a participant.
type IncomingCommand struct {
	Incoming Incoming
	EventID  string
	Planner  Planner
}

// TransferView is a transfer as the lab sees it in the truth.
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

// IncomingView is an incoming PIX as the lab sees it in the truth.
type IncomingView struct {
	EndToEndID    string    `json:"end_to_end_id"`
	ParticipantID string    `json:"participant_id"`
	AccountID     string    `json:"account_id"`
	AmountCents   int64     `json:"amount_cents"`
	CreatedAt     time.Time `json:"created_at"`
}

// Export is the whole truth of one participant, double processing included:
// what the checker compares the bank against.
type Export struct {
	ParticipantID string           `json:"participant_id"`
	Transfers     []TransferView   `json:"transfers"`
	Statement     []StatementEntry `json:"statement"`
	Incoming      []IncomingView   `json:"incoming"`
	// PendingDeliveries is how many webhooks are still to be sent.
	PendingDeliveries int `json:"pending_deliveries"`
}
