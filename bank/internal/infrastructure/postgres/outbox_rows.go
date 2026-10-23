package postgres

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

const (
	outboxMessagesTable = "outbox_messages"
	inboxEventsTable    = "inbox_events"
)

// outboxMessageRow is what enqueueing writes to outbox_messages; the
// lease, attempts and status columns start at their defaults.
type outboxMessageRow struct {
	Topic         string    `db:"topic"`
	AggregateID   uuid.UUID `db:"aggregate_id"`
	Payload       []byte    `db:"payload"`
	CorrelationID string    `db:"correlation_id"`
}

// outboxClaimRow is a leased message as the claim returns it.
type outboxClaimRow struct {
	ID            int64     `db:"id"`
	AggregateID   uuid.UUID `db:"aggregate_id"`
	LeaseToken    int64     `db:"lease_token"`
	Attempts      int       `db:"attempts"`
	CorrelationID string    `db:"correlation_id"`
}

// inboxEventRow is what the intake writes to inbox_events.
type inboxEventRow struct {
	EventID       string `db:"event_id"`
	EventType     string `db:"event_type"`
	EndToEndID    string `db:"end_to_end_id"`
	Payload       []byte `db:"payload"`
	CorrelationID string `db:"correlation_id"`
}

// inboxClaimRow is a claimed event as the claim returns it.
type inboxClaimRow struct {
	ID int64 `db:"id"`

	inboxEventRow
}

// inboxPayload is the part of a partner event kept as JSON.
type inboxPayload struct {
	AmountCents int64  `json:"amount_cents"`
	Sequence    int    `json:"sequence"`
	Reason      string `json:"reason"`
	AccountID   string `json:"account_id"`
}

func outboxMessageRowOf(message application.OutboxMessage) outboxMessageRow {
	return outboxMessageRow{
		Topic: message.Topic, AggregateID: message.AggregateID, Payload: message.Payload, CorrelationID: message.CorrelationID,
	}
}

func (row outboxClaimRow) toDomain() application.OutboxClaim {
	return application.OutboxClaim{
		ID: row.ID, AggregateID: row.AggregateID, Token: row.LeaseToken, Attempts: row.Attempts, CorrelationID: row.CorrelationID,
	}
}

func inboxEventRowOf(event application.PartnerEvent, correlationID string) (inboxEventRow, error) {
	fields := inboxPayload{AmountCents: int64(event.AmountCents), Sequence: event.Sequence, Reason: event.Reason, AccountID: event.AccountID}

	payload, err := json.Marshal(fields)
	if err != nil {
		return inboxEventRow{}, err
	}

	return inboxEventRow{
		EventID: event.ID, EventType: string(event.Type), EndToEndID: string(event.EndToEndID), Payload: payload, CorrelationID: correlationID,
	}, nil
}

// toDomain decodes the event the inbox kept.
func (row inboxClaimRow) toDomain() (application.InboxEvent, error) {
	var fields inboxPayload

	err := json.Unmarshal(row.Payload, &fields)
	if err != nil {
		return application.InboxEvent{}, fmt.Errorf("decoding inbox event %d: %w", row.ID, err)
	}

	event := application.PartnerEvent{
		ID: row.EventID, Type: application.EventType(row.EventType), EndToEndID: transfer.EndToEndID(row.EndToEndID),
		AmountCents: money.Cents(fields.AmountCents), Sequence: fields.Sequence, Reason: fields.Reason, AccountID: fields.AccountID,
	}

	return application.InboxEvent{ID: row.ID, Event: event, CorrelationID: row.CorrelationID}, nil
}
