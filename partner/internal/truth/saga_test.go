package truth_test

import (
	"context"
	"testing"
	"time"

	"centavo/partner/internal/truth"
)

var epoch = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

type memoryTx struct {
	transfers  map[string]truth.Transfer
	entries    []truth.StatementEntry
	deliveries []truth.DeliveryRecord
}

func TestSaga_SettlesOnceOrTwiceWhenTheBugHits(t *testing.T) {
	t.Parallel()

	for _, isDouble := range []bool{false, true} {
		transfer := truth.Transfer{EndToEndID: "E1", Status: truth.StatusProcessing, AmountCents: 100, IsDoubleProcessed: isDouble}
		memory := newMemory(transfer)

		command := truth.SettleCommand{EndToEndID: "E1", Now: epoch, EventID: "evt", Planner: honest}

		err := truth.NewSaga(memory).Settle(t.Context(), command)
		if err != nil {
			t.Fatal(err)
		}

		want := 1
		if isDouble {
			want = 2
		}

		isUnexpected := len(memory.entries) != want || len(memory.deliveries) != 1 || memory.transfers["E1"].Status != truth.StatusSettled
		if isUnexpected {
			t.Fatalf("double %v: %d entries, %d deliveries, %s", isDouble, len(memory.entries), len(memory.deliveries), memory.transfers["E1"].Status)
		}
	}
}

func TestSaga_FollowsTheStateMachine(t *testing.T) {
	t.Parallel()

	rejected := truth.Transfer{EndToEndID: "E-rejected", Status: truth.StatusRejected}
	processing := truth.Transfer{EndToEndID: "E-processing", Status: truth.StatusProcessing}
	memory := newMemory(rejected, processing)
	saga := truth.NewSaga(memory)

	errs := []error{
		saga.Settle(t.Context(), truth.SettleCommand{EndToEndID: "E-rejected", Now: epoch, EventID: "e1", Planner: honest}),
		saga.Return(t.Context(), truth.ReturnCommand{EndToEndID: "E-processing", Now: epoch, EventID: "e2", Planner: honest}),
	}

	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	isUntouched := len(memory.entries) == 0 && len(memory.deliveries) == 0 && memory.transfers["E-processing"].Status == truth.StatusProcessing
	if !isUntouched {
		t.Fatalf("entries %v, deliveries %v, transfers %v", memory.entries, memory.deliveries, memory.transfers)
	}
}

func (m *memoryTx) InTx(ctx context.Context, fn func(ctx context.Context, tx truth.Tx) error) error {
	return fn(ctx, m)
}

func (m *memoryTx) FindTransfer(_ context.Context, endToEndID string) (truth.Transfer, error) {
	transfer, isKnown := m.transfers[endToEndID]
	if !isKnown {
		return truth.Transfer{}, truth.ErrNotFound
	}

	return transfer, nil
}

func (m *memoryTx) InsertTransfer(_ context.Context, transfer truth.Transfer) error {
	m.transfers[transfer.EndToEndID] = transfer

	return nil
}

func (m *memoryTx) MarkSettled(_ context.Context, endToEndID string, returnAt *time.Time) error {
	transfer := m.transfers[endToEndID]
	transfer.Status, transfer.ReturnAt = truth.StatusSettled, returnAt
	m.transfers[endToEndID] = transfer

	return nil
}

func (m *memoryTx) MarkRejected(_ context.Context, endToEndID, reason string) error {
	transfer := m.transfers[endToEndID]
	transfer.Status, transfer.RejectReason = truth.StatusRejected, reason
	m.transfers[endToEndID] = transfer

	return nil
}

func (m *memoryTx) MarkReturned(_ context.Context, endToEndID string) error {
	transfer := m.transfers[endToEndID]
	transfer.Status = truth.StatusReturned
	m.transfers[endToEndID] = transfer

	return nil
}

func (m *memoryTx) InsertIncoming(context.Context, truth.Incoming) (bool, error) {
	return true, nil
}

func (m *memoryTx) AddEntry(_ context.Context, entry truth.StatementEntry) error {
	m.entries = append(m.entries, entry)

	return nil
}

func (m *memoryTx) NextWebhookSeq(context.Context, string) (int, error) {
	return len(m.deliveries) + 1, nil
}

func (m *memoryTx) AddDelivery(_ context.Context, delivery truth.DeliveryRecord) error {
	m.deliveries = append(m.deliveries, delivery)

	return nil
}

func honest(event truth.Event, now time.Time) []truth.PlannedDelivery {
	return []truth.PlannedDelivery{{EventID: event.ID, At: now}}
}

func newMemory(transfers ...truth.Transfer) *memoryTx {
	memory := &memoryTx{transfers: map[string]truth.Transfer{}}
	for _, transfer := range transfers {
		memory.transfers[transfer.EndToEndID] = transfer
	}

	return memory
}
