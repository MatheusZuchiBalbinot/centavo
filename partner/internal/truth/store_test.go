package truth_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"centavo/partner/internal/truth"
)

var epoch = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func honest(event truth.Event, now time.Time) []truth.PlannedDelivery {
	return []truth.PlannedDelivery{{EventID: event.ID, At: now}}
}

func openStore(t *testing.T) *truth.Store {
	t.Helper()

	store, err := truth.Open(t.Context(), filepath.Join(t.TempDir(), "truth.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	return store
}

func submit(t *testing.T, store *truth.Store, e2e string, isDouble bool) truth.SubmitResult {
	t.Helper()

	transfer := truth.Transfer{EndToEndID: e2e, ParticipantID: "centavo-robust", AmountCents: 4_500, DestinationKey: "dest-0001", Status: truth.StatusProcessing, IsDoubleProcessed: isDouble, SettleAt: epoch, CreatedAt: epoch}

	result, err := store.Submit(t.Context(), transfer)
	if err != nil {
		t.Fatal(err)
	}

	return result
}

func TestSubmit_IsIdempotentByEndToEndID(t *testing.T) {
	store := openStore(t)

	first := submit(t, store, "E1", false)
	again := submit(t, store, "E1", false)

	if !first.IsNew || again.IsNew || again.Transfer.Status != truth.StatusProcessing {
		t.Fatalf("first %+v, again %+v", first, again)
	}
}

func TestSettle_WritesTheStatementOnceOrTwiceWhenTheBugHits(t *testing.T) {
	store := openStore(t)
	submit(t, store, "E-once", false)
	submit(t, store, "E-twice", true)

	for _, e2e := range []string{"E-once", "E-twice", "E-once"} {
		err := store.Settle(t.Context(), truth.SettleCommand{EndToEndID: e2e, Now: epoch, EventID: "evt-" + e2e, Planner: honest})
		if err != nil {
			t.Fatal(err)
		}
	}

	export, err := store.Truth(t.Context(), "centavo-robust")
	if err != nil {
		t.Fatal(err)
	}

	settlements := map[string]int{}
	for _, entry := range export.Statement {
		settlements[entry.EndToEndID]++
	}

	if settlements["E-once"] != 1 || settlements["E-twice"] != 2 {
		t.Fatalf("settlements: %v", settlements)
	}

	deliveries, err := store.DueDeliveries(t.Context(), epoch, 10)
	if err != nil || len(deliveries) != 2 {
		t.Fatalf("deliveries: %d, %v (a second settle of the same transfer must not notify)", len(deliveries), err)
	}
}

func TestRejectAndReturn_FollowTheStatusMachine(t *testing.T) {
	store := openStore(t)
	submit(t, store, "E-rejected", false)
	submit(t, store, "E-returned", false)

	ctx := context.Background()

	err := errors.Join(
		store.Reject(ctx, truth.RejectCommand{EndToEndID: "E-rejected", Reason: "invalid_key", Now: epoch, EventID: "e1", Planner: honest}),
		store.Return(ctx, truth.ReturnCommand{EndToEndID: "E-returned", Now: epoch, EventID: "e2", Planner: honest}),
		store.Settle(ctx, truth.SettleCommand{EndToEndID: "E-returned", Now: epoch, EventID: "e3", Planner: honest}),
		store.Return(ctx, truth.ReturnCommand{EndToEndID: "E-returned", Now: epoch, EventID: "e4", Planner: honest}),
		store.Settle(ctx, truth.SettleCommand{EndToEndID: "E-rejected", Now: epoch, EventID: "e5", Planner: honest}),
	)
	if err != nil {
		t.Fatal(err)
	}

	rejected, _ := store.Get(ctx, "E-rejected")
	returned, _ := store.Get(ctx, "E-returned")

	if rejected.Status != truth.StatusRejected || returned.Status != truth.StatusReturned {
		t.Fatalf("rejected is %s, returned is %s", rejected.Status, returned.Status)
	}

	statement, err := store.Statement(ctx, "centavo-robust", 0, 10)
	if err != nil {
		t.Fatal(err)
	}

	isOrdered := len(statement) == 2 && statement[0].Kind == truth.EntrySettlement && statement[1].Kind == truth.EntryReturn && statement[0].Seq < statement[1].Seq
	if !isOrdered {
		t.Fatalf("statement: %+v (a return before the settlement and a settle after a reject must do nothing)", statement)
	}
}

func TestAddIncoming_CreditsOncePerEndToEndID(t *testing.T) {
	store := openStore(t)
	incoming := truth.Incoming{EndToEndID: "E-in", ParticipantID: "centavo-robust", AccountID: "acc-1", AmountCents: 900, CreatedAt: epoch}

	for range 2 {
		_, err := store.AddIncoming(t.Context(), truth.IncomingCommand{Incoming: incoming, EventID: "evt-in", Planner: honest})
		if err != nil {
			t.Fatal(err)
		}
	}

	export, err := store.Truth(t.Context(), "centavo-robust")
	if err != nil {
		t.Fatal(err)
	}

	if len(export.Incoming) != 1 || len(export.Statement) != 1 {
		t.Fatalf("incoming %d, statement %d", len(export.Incoming), len(export.Statement))
	}
}

func TestDeliveries_CanBeDroppedRescheduledAndAbandoned(t *testing.T) {
	store := openStore(t)
	submit(t, store, "E1", false)
	submit(t, store, "E2", false)

	dropping := func(event truth.Event, now time.Time) []truth.PlannedDelivery {
		return []truth.PlannedDelivery{{EventID: event.ID, At: now, IsDropped: true}}
	}

	twice := func(event truth.Event, now time.Time) []truth.PlannedDelivery {
		return []truth.PlannedDelivery{{EventID: event.ID, At: now}, {EventID: event.ID + "-copy", At: now.Add(time.Second)}}
	}

	err := errors.Join(
		store.Settle(t.Context(), truth.SettleCommand{EndToEndID: "E1", Now: epoch, EventID: "dropped", Planner: dropping}),
		store.Settle(t.Context(), truth.SettleCommand{EndToEndID: "E2", Now: epoch, EventID: "doubled", Planner: twice}),
	)
	if err != nil {
		t.Fatal(err)
	}

	now, err := store.DueDeliveries(t.Context(), epoch, 10)
	if err != nil || len(now) != 1 || now[0].EventID != "doubled" {
		t.Fatalf("due now: %+v, %v", now, err)
	}

	err = store.Reschedule(t.Context(), now[0].ID, epoch.Add(time.Hour), true)
	if err != nil {
		t.Fatal(err)
	}

	later, err := store.DueDeliveries(t.Context(), epoch.Add(2*time.Hour), 10)
	if err != nil || len(later) != 1 || later[0].EventID != "doubled-copy" {
		t.Fatalf("due later: %+v, %v (the abandoned delivery must not come back)", later, err)
	}

	pending, err := store.PendingDeliveries(t.Context())
	if err != nil || pending != 1 {
		t.Fatalf("pending: %d, %v", pending, err)
	}
}

// TestOpen_AddsTheColumnsOfANewerSchema guards the partner's volume across
// upgrades: a truth created before a column existed gets it on open.
func TestOpen_AddsTheColumnsOfANewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "truth.db")

	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = old.ExecContext(t.Context(), `CREATE TABLE transfers (end_to_end_id TEXT PRIMARY KEY, participant_id TEXT NOT NULL, amount_cents INTEGER NOT NULL, destination_key TEXT NOT NULL, status TEXT NOT NULL, reject_reason TEXT NOT NULL DEFAULT '', is_double INTEGER NOT NULL DEFAULT 0, webhook_seq INTEGER NOT NULL DEFAULT 0, settle_at INTEGER NOT NULL, return_at INTEGER, created_at INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}

	err = old.Close()
	if err != nil {
		t.Fatal(err)
	}

	store, err := truth.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	transfer := truth.Transfer{EndToEndID: "E-old", ParticipantID: "centavo-robust", AmountCents: 100, DestinationKey: "dest-0001", Status: truth.StatusProcessing, SettleAt: epoch, CreatedAt: epoch, CorrelationID: "r0001-p01-000001"}

	_, err = store.Submit(t.Context(), transfer)
	if err != nil {
		t.Fatal(err)
	}

	stored, err := store.Get(t.Context(), "E-old")
	if err != nil || stored.CorrelationID != transfer.CorrelationID {
		t.Fatalf("stored %+v, %v", stored, err)
	}
}
