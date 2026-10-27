package storage_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"centavo/partner/internal/storage"
	"centavo/partner/internal/truth"
)

var epoch = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

type truthStore struct {
	*storage.DB
	truth.Saga
	truth.Intake
}

func TestSubmit_IsIdempotentByEndToEndID(t *testing.T) {
	t.Parallel()

	store := openStore(t)

	first := submit(t, store, "E1", false)
	again := submit(t, store, "E1", false)

	isUnexpected := !first.IsNew || again.IsNew || again.Transfer.Status != truth.StatusProcessing
	if isUnexpected {
		t.Fatalf("first %+v, again %+v", first, again)
	}
}

func TestSettle_WritesTheStatementOnceOrTwiceWhenTheBugHits(t *testing.T) {
	t.Parallel()

	store := openStore(t)
	submit(t, store, "E-once", false)
	submit(t, store, "E-twice", true)

	for _, e2e := range []string{"E-once", "E-twice", "E-once"} {
		err := store.Settle(t.Context(), truth.SettleCommand{EndToEndID: e2e, Now: epoch, EventID: "evt-" + e2e, Planner: honest})
		if err != nil {
			t.Fatal(err)
		}
	}

	export, err := store.Truth(t.Context(), "centavo-robust", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	settlements := map[string]int{}
	for _, entry := range export.Statement {
		settlements[entry.EndToEndID]++
	}

	isUnexpected := settlements["E-once"] != 1 || settlements["E-twice"] != 2
	if isUnexpected {
		t.Fatalf("settlements: %v", settlements)
	}

	deliveries, err := store.DueDeliveries(t.Context(), epoch, 10)
	if err != nil {
		t.Fatalf("deliveries: %d, %v (a second settle of the same transfer must not notify)", len(deliveries), err)
	}

	if len(deliveries) != 2 {
		t.Fatalf("deliveries: %d, %v (a second settle of the same transfer must not notify)", len(deliveries), err)
	}
}

func TestRejectAndReturn_FollowTheStatusMachine(t *testing.T) {
	t.Parallel()

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

	isUnexpected := rejected.Status != truth.StatusRejected || returned.Status != truth.StatusReturned
	if isUnexpected {
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
	t.Parallel()

	store := openStore(t)
	incoming := truth.Incoming{EndToEndID: "E-in", ParticipantID: "centavo-robust", AccountID: "acc-1", AmountCents: 900, CreatedAt: epoch}

	for range 2 {
		_, err := store.AddIncoming(t.Context(), truth.IncomingCommand{Incoming: incoming, EventID: "evt-in", Planner: honest})
		if err != nil {
			t.Fatal(err)
		}
	}

	export, err := store.Truth(t.Context(), "centavo-robust", time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	isUnexpected := len(export.Incoming) != 1 || len(export.Statement) != 1
	if isUnexpected {
		t.Fatalf("incoming %d, statement %d", len(export.Incoming), len(export.Statement))
	}

	statement, err := store.Statement(t.Context(), "centavo-robust", 0, 10)
	if err != nil {
		t.Fatal(err)
	}

	// The reconciler credits a PIX in whose webhook never came from this.
	if statement[0].DestinationAccountID != "acc-1" {
		t.Fatalf("incoming entry without its account: %+v", statement[0])
	}
}

func TestDeliveries_CanBeDroppedRescheduledAndAbandoned(t *testing.T) {
	t.Parallel()

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
	if err != nil {
		t.Fatalf("due now: %+v, %v", now, err)
	}

	isUnexpected := len(now) != 1 || now[0].EventID != "doubled"
	if isUnexpected {
		t.Fatalf("due now: %+v, %v", now, err)
	}

	err = store.Abandon(t.Context(), now[0].ID, epoch.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}

	later, err := store.DueDeliveries(t.Context(), epoch.Add(2*time.Hour), 10)
	if err != nil {
		t.Fatalf("due later: %+v, %v (the abandoned delivery must not come back)", later, err)
	}

	isUnexpected = len(later) != 1 || later[0].EventID != "doubled-copy"
	if isUnexpected {
		t.Fatalf("due later: %+v, %v (the abandoned delivery must not come back)", later, err)
	}

	pending, err := store.PendingDeliveries(t.Context())
	if err != nil {
		t.Fatalf("pending: %d, %v", pending, err)
	}

	if pending != 1 {
		t.Fatalf("pending: %d, %v", pending, err)
	}
}

func TestOpen_AddsTheColumnsOfANewerSchema(t *testing.T) {
	t.Parallel()

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

	store := openStoreAt(t, path)

	transfer := truth.Transfer{EndToEndID: "E-old", ParticipantID: "centavo-robust", AmountCents: 100, DestinationKey: "dest-0001", Status: truth.StatusProcessing, SettleAt: epoch, CreatedAt: epoch, CorrelationID: "r0001-p01-000001"}

	_, err = store.Submit(t.Context(), transfer)
	if err != nil {
		t.Fatal(err)
	}

	stored, err := store.Get(t.Context(), "E-old")
	if err != nil {
		t.Fatalf("stored %+v, %v", stored, err)
	}

	if stored.CorrelationID != transfer.CorrelationID {
		t.Fatalf("stored %+v, %v", stored, err)
	}
}

func TestTruth_ExportsOnlyWhatCameAfterSince(t *testing.T) {
	t.Parallel()

	store := openStore(t)
	submit(t, store, "E-before", false)

	later := epoch.Add(time.Hour)
	transfer := truth.Transfer{EndToEndID: "E-after", ParticipantID: "centavo-robust", AmountCents: 4_500, DestinationKey: "dest-0001", Status: truth.StatusProcessing, SettleAt: later, CreatedAt: later}

	_, err := store.Submit(t.Context(), transfer)
	if err != nil {
		t.Fatal(err)
	}

	settledAt := map[string]time.Time{"E-before": epoch, "E-after": later}

	for e2e, now := range settledAt {
		err := store.Settle(t.Context(), truth.SettleCommand{EndToEndID: e2e, Now: now, EventID: "evt-" + e2e, Planner: honest})
		if err != nil {
			t.Fatal(err)
		}
	}

	export, err := store.Truth(t.Context(), "centavo-robust", later)
	if err != nil {
		t.Fatal(err)
	}

	isOnlyTheLaterOne := len(export.Transfers) == 1 && export.Transfers[0].EndToEndID == "E-after" && len(export.Statement) == 1 && export.Statement[0].EndToEndID == "E-after"
	if !isOnlyTheLaterOne {
		t.Fatalf("export since %s: %+v", later, export)
	}
}

// The bank's reconciler failpoints match the PIX's correlation id, and the
// statement is all the reconciler reads.
func TestStatement_CarriesTheCorrelationOfEachPix(t *testing.T) {
	t.Parallel()

	store := openStore(t)
	transfer := truth.Transfer{EndToEndID: "E-out", ParticipantID: "centavo-robust", AmountCents: 4_500, DestinationKey: "dest-0001", Status: truth.StatusProcessing, SettleAt: epoch, CreatedAt: epoch, CorrelationID: "r0001-p01-000001"}

	_, err := store.Submit(t.Context(), transfer)
	if err != nil {
		t.Fatal(err)
	}

	err = store.Settle(t.Context(), truth.SettleCommand{EndToEndID: "E-out", Now: epoch, EventID: "evt-out", Planner: honest})
	if err != nil {
		t.Fatal(err)
	}

	incoming := truth.Incoming{EndToEndID: "E-in", ParticipantID: "centavo-robust", AccountID: "acc-1", AmountCents: 900, CreatedAt: epoch, CorrelationID: "r0001-p02-000001"}

	_, err = store.AddIncoming(t.Context(), truth.IncomingCommand{Incoming: incoming, EventID: "evt-in", Planner: honest})
	if err != nil {
		t.Fatal(err)
	}

	entries, err := store.Statement(t.Context(), "centavo-robust", 0, 10)
	if err != nil {
		t.Fatal(err)
	}

	correlations := map[string]string{}
	for _, entry := range entries {
		correlations[entry.EndToEndID] = entry.CorrelationID
	}

	isUnexpected := correlations["E-out"] != "r0001-p01-000001" || correlations["E-in"] != "r0001-p02-000001"
	if isUnexpected {
		t.Fatalf("statement correlations: %v", correlations)
	}
}

func honest(event truth.Event, now time.Time) []truth.PlannedDelivery {
	return []truth.PlannedDelivery{{EventID: event.ID, At: now}}
}

func openStore(t *testing.T) truthStore {
	t.Helper()

	return openStoreAt(t, filepath.Join(t.TempDir(), "truth.db"))
}

func openStoreAt(t *testing.T, path string) truthStore {
	t.Helper()

	db, err := storage.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return truthStore{DB: db, Saga: truth.NewSaga(db), Intake: truth.NewIntake(db)}
}

func submit(t *testing.T, store truthStore, e2e string, isDouble bool) truth.SubmitResult {
	t.Helper()

	transfer := truth.Transfer{EndToEndID: e2e, ParticipantID: "centavo-robust", AmountCents: 4_500, DestinationKey: "dest-0001", Status: truth.StatusProcessing, IsDoubleProcessed: isDouble, SettleAt: epoch, CreatedAt: epoch}

	result, err := store.Submit(t.Context(), transfer)
	if err != nil {
		t.Fatal(err)
	}

	return result
}
