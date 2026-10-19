package truth

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	// singleSettlement is what an honest partner books for a transfer.
	singleSettlement = 1
	// doubleSettlement is what the partner's own bug books instead.
	doubleSettlement = 2
)

// canSettle and the next two are the partner's state machine: a transfer is
// settled or rejected only while processing, and returned only once settled.
func (t Transfer) canSettle() bool { return t.Status == StatusProcessing }

func (t Transfer) canReject() bool { return t.Status == StatusProcessing }

func (t Transfer) canReturn() bool { return t.Status == StatusSettled }

// settlements is how many statement entries settling the transfer books.
func (t Transfer) settlements() int {
	if t.IsDoubleProcessed {
		return doubleSettlement
	}

	return singleSettlement
}

// entry is the statement line a transfer books for kind.
func (t Transfer) entry(kind EntryKind, now time.Time) StatementEntry {
	return StatementEntry{ParticipantID: t.ParticipantID, Kind: kind, EndToEndID: t.EndToEndID, AmountCents: t.AmountCents, CreatedAt: now}
}

// event is the webhook a transfer owes after a decision.
func (t Transfer) event(eventType EventType, eventID string, now time.Time) Event {
	return Event{
		ID: eventID, Type: eventType, EndToEndID: t.EndToEndID, AmountCents: t.AmountCents, OccurredAt: now,
		ParticipantID: t.ParticipantID, CorrelationID: t.CorrelationID,
	}
}

// entry is the statement line an incoming PIX books.
func (i Incoming) entry() StatementEntry {
	return StatementEntry{ParticipantID: i.ParticipantID, Kind: EntryIncoming, EndToEndID: i.EndToEndID, AmountCents: i.AmountCents, CreatedAt: i.CreatedAt}
}

// event is the webhook that announces an incoming PIX.
func (i Incoming) event(eventID string) Event {
	return Event{
		ID: eventID, Type: EventIncoming, EndToEndID: i.EndToEndID, AmountCents: i.AmountCents, OccurredAt: i.CreatedAt,
		AccountID: i.AccountID, ParticipantID: i.ParticipantID, CorrelationID: i.CorrelationID,
	}
}

// planDeliveries turns an event into the deliveries the planner decided: one
// now when honest, or late, twice, under a new id or dropped when it lies.
func planDeliveries(event Event, planner Planner) ([]DeliveryRecord, error) {
	planned := planner(event, event.OccurredAt)
	records := make([]DeliveryRecord, 0, len(planned))

	for _, delivery := range planned {
		copyOf := event
		copyOf.ID = delivery.EventID

		payload, err := json.Marshal(copyOf)
		if err != nil {
			return nil, fmt.Errorf("encoding a webhook: %w", err)
		}

		status := DeliveryPending
		if delivery.IsDropped {
			status = DeliveryAbandoned
		}

		record := DeliveryRecord{
			EventID: copyOf.ID, ParticipantID: event.ParticipantID, Payload: payload, At: delivery.At, Status: status,
			CorrelationID: event.CorrelationID,
		}
		records = append(records, record)
	}

	return records, nil
}
