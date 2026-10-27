package truth

import (
	"encoding/json"
	"fmt"
	"time"
)

const (
	singleSettlement = 1
	doubleSettlement = 2
)

// The partner's state machine: a transfer is settled or rejected only while
// processing, and returned only once settled.
func (t Transfer) canSettle() bool { return t.Status == StatusProcessing }

func (t Transfer) canReject() bool { return t.Status == StatusProcessing }

func (t Transfer) canReturn() bool { return t.Status == StatusSettled }

func (t Transfer) settlements() int {
	if t.IsDoubleProcessed {
		return doubleSettlement
	}

	return singleSettlement
}

func (t Transfer) entry(kind EntryKind, now time.Time) StatementEntry {
	return StatementEntry{ParticipantID: t.ParticipantID, Kind: kind, EndToEndID: t.EndToEndID, AmountCents: t.AmountCents, CreatedAt: now}
}

func (t Transfer) event(eventType EventType, eventID string, now time.Time) Event {
	return Event{
		ID: eventID, Type: eventType, EndToEndID: t.EndToEndID, AmountCents: t.AmountCents, OccurredAt: now,
		ParticipantID: t.ParticipantID, CorrelationID: t.CorrelationID,
	}
}

func (i Incoming) entry() StatementEntry {
	return StatementEntry{ParticipantID: i.ParticipantID, Kind: EntryIncoming, EndToEndID: i.EndToEndID, AmountCents: i.AmountCents, CreatedAt: i.CreatedAt}
}

func (i Incoming) event(eventID string) Event {
	return Event{
		ID: eventID, Type: EventIncoming, EndToEndID: i.EndToEndID, AmountCents: i.AmountCents, OccurredAt: i.CreatedAt,
		AccountID: i.AccountID, ParticipantID: i.ParticipantID, CorrelationID: i.CorrelationID,
	}
}

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
