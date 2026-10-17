//go:build chaoslab

package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/infrastructure/postgres"
	"centavo/bank/internal/platform/clock"
	"centavo/bank/internal/platform/correlation"
	"centavo/bank/internal/platform/defenses"
	"centavo/bank/internal/platform/failpoint"
	_ "centavo/bank/internal/platform/naive"
)

// armFP01 makes FP01 fail every transfer of one correlation id. The error
// action stands in for the kill: either way the use case stops between the
// debit and the credit, and a real transaction rolls back.
func armFP01(t *testing.T, correlationID string) {
	t.Helper()

	failpoint.DisarmAll()
	failpoint.ConfigureEvents(failpoint.EventsConfig{Dir: t.TempDir(), Service: "bank-api", Instance: "test"})
	t.Cleanup(failpoint.DisarmAll)

	rule := failpoint.Rule{Action: failpoint.ActionError, Arg: "crash", Probability: 1, Match: failpoint.Match{CorrelationID: correlationID}, TTL: "1m"}

	err := failpoint.Arm(failpoint.TransferAfterDebitBeforeCredit, rule, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

// useSchema gives the database the constraints of a weakened profile for one
// test, as bank migrate would, and restores the robust schema afterwards.
func useSchema(t *testing.T, wanted schema) {
	t.Helper()

	err := migrateSchema(t.Context(), wanted)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		err := migrateDefenses(context.Background(), true)
		if err != nil {
			t.Fatalf("restoring the robust schema: %v", err)
		}
	})
}

func naiveMoney(t *testing.T, b bank, profile string) application.MoneyDependencies {
	t.Helper()

	parsed, err := defenses.ParseProfile(profile)
	if err != nil {
		t.Fatal(err)
	}

	deps := application.MoneyDependencies{UnitOfWork: postgres.NewUnitOfWork(b.pool), Locker: application.RowLocker{}}
	variantDeps := defenses.Dependencies{Pool: b.pool}

	if !parsed.IsEnabled(defenses.AtomicLedger) {
		deps.UnitOfWork, err = defenses.Naive[application.UnitOfWork](defenses.AtomicLedger, variantDeps)
		if err != nil {
			t.Fatal(err)
		}
	}

	if !parsed.IsEnabled(defenses.BalanceLocking) {
		deps.Locker, err = defenses.Naive[application.Locker](defenses.BalanceLocking, variantDeps)
		if err != nil {
			t.Fatal(err)
		}
	}

	return deps
}

// TestInternalTransfer_CrashBetweenDebitAndCredit_LeavesNoPartialEntries is
// the regression test of PM-002, with its positive control.
func TestInternalTransfer_CrashBetweenDebitAndCredit_LeavesNoPartialEntries(t *testing.T) {
	cases := []struct {
		name            string
		transfer        func(b bank) application.TransferInternal
		wantPartialLeft bool
	}{
		{name: "robust: the crash rolls everything back", transfer: func(b bank) application.TransferInternal { return b.transfer }},
		{name: "without atomic_ledger: the debit stays", transfer: func(b bank) application.TransferInternal {
			useSchema(t, schema{hasAtomicLedger: false, hasBalanceLocking: true, hasIdempotency: false})

			return application.NewTransferInternal(naiveMoney(t, b, "robust,-atomic_ledger"))
		}, wantPartialLeft: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newBank(t)
			source, destination := b.newCustomer(t), b.newCustomer(t)
			b.fund(t, source, 1_000)

			write := newWrite(t)
			armFP01(t, write.CorrelationID)

			ctx := correlation.With(context.Background(), write.CorrelationID)
			input := application.TransferInput{From: source, To: destination, Amount: amountOf(t, 300), Write: write}

			_, err := tc.transfer(b).Execute(ctx, input)
			if !errors.Is(err, failpoint.ErrSynthetic) {
				t.Fatalf("got %v, want the injected crash", err)
			}

			partial := b.transactionsWithClientRef(t, write.Key)

			if tc.wantPartialLeft {
				if partial != 1 || len(b.statement(t, source)) != 2 || len(b.statement(t, destination)) != 0 {
					t.Fatalf("expected a debit without credit, got %d transactions", partial)
				}

				return
			}

			if partial != 0 {
				t.Fatalf("the crash left %d ledger transactions behind", partial)
			}

			assertChainedAndNeverNegative(t, b, source, 1_000)
		})
	}
}

// TestBalance_ConcurrentDebits_WithoutBalanceLocking is the positive control
// of PM-001: without D2 the same concurrent debits are no longer serialized,
// and the safety net in the database refuses some of the conflicting writes.
func TestBalance_ConcurrentDebits_WithoutBalanceLocking(t *testing.T) {
	b := newBank(t)
	source, destination := b.newCustomer(t), b.newCustomer(t)
	b.fund(t, source, 1_000)

	transfer := application.NewTransferInternal(naiveMoney(t, b, "robust,-balance_locking"))

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		failures int
	)

	for range 20 {
		wg.Go(func() {
			input := application.TransferInput{From: source, To: destination, Amount: amountOf(t, 100), Write: newWrite(t)}

			_, err := transfer.Execute(context.Background(), input)

			isUnexpected := err != nil && !errors.Is(err, ledger.ErrInsufficientFunds)
			if isUnexpected {
				mu.Lock()
				failures++
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	if failures == 0 {
		t.Fatal("without row locks, concurrent debits never collided; the control proves nothing")
	}
}

// TestInbox_CrashBetweenApplyAndMark_AppliesOnce is FC07: the processor
// stopped after applying an event and before marking it (FP06). The effect
// and the mark share the transaction, so the next pass applies it once.
func TestInbox_CrashBetweenApplyAndMark_AppliesOnce(t *testing.T) {
	p := newOutboxBank(t)
	intake, processor := newInbox(p, statusAnswer(transfer.PartnerSettled))
	drainInbox(t, processor)

	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	created := p.createPixOut(t, account, 4_500)
	p.markSent(t, created.TransferID)
	settled := application.PartnerEvent{ID: "evt_fp06_" + created.EndToEndID, Type: application.EventSettled, EndToEndID: created.EndToEndID, AmountCents: 4_500}

	err := intake.Apply(correlation.With(t.Context(), settled.ID), settled)
	if err != nil {
		t.Fatal(err)
	}

	failpoint.DisarmAll()
	failpoint.ConfigureEvents(failpoint.EventsConfig{Dir: t.TempDir(), Service: "bank-worker", Instance: "test"})
	t.Cleanup(failpoint.DisarmAll)

	rule := failpoint.Rule{Action: failpoint.ActionError, Arg: "crash", Probability: 1, MaxFires: 1, Match: failpoint.Match{CorrelationID: settled.ID}, TTL: "1m"}

	err = failpoint.Arm(failpoint.InboxAfterApplyBeforeMark, rule, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	drainInbox(t, processor)

	pending := countRows(t, p, `SELECT count(*) FROM inbox_events WHERE event_id = $1 AND status = 'pending'`, settled.ID)
	if pending != 1 {
		t.Fatal("the crashed event left the inbox")
	}

	_, err = p.bank.pool.Exec(t.Context(), `UPDATE inbox_events SET next_attempt_at = now() WHERE event_id = $1`, settled.ID)
	if err != nil {
		t.Fatal(err)
	}

	drainInbox(t, processor)

	settlements := countRows(t, p, `SELECT count(*) FROM ledger_transactions WHERE external_transfer_id = $1 AND kind = 'pix_out_settle'`, uuid.UUID(created.TransferID))
	if settlements != 1 {
		t.Fatalf("%d settlements after a crash between apply and mark, want 1", settlements)
	}
}

// armFP08 fails the reconciliation once, between two entries of a batch.
func armFP08(t *testing.T) {
	t.Helper()

	failpoint.DisarmAll()
	failpoint.ConfigureEvents(failpoint.EventsConfig{Dir: t.TempDir(), Service: "bank-worker", Instance: "test"})
	t.Cleanup(failpoint.DisarmAll)

	rule := failpoint.Rule{Action: failpoint.ActionError, Arg: "crash", Probability: 1, MaxFires: 1, SkipHits: 1, TTL: "1m"}

	err := failpoint.Arm(failpoint.ReconMidApply, rule, time.Now())
	if err != nil {
		t.Fatal(err)
	}
}

// TestReconciliation_CrashMidBatch_AppliesEachEntryOnce is FC09 and PM-011:
// the reconciler died between two entries (FP08). With D10 the batch rolls
// back as a whole and the rerun books each entry once; the naive variant
// had committed the first entry and books it again.
func TestReconciliation_CrashMidBatch_AppliesEachEntryOnce(t *testing.T) {
	cases := []struct {
		name     string
		policy   application.BatchPolicy
		schema   schema
		wantOwed int64
	}{
		{name: "atomic batches", policy: application.AtomicBatches{}, schema: schema{hasAtomicLedger: true, hasBalanceLocking: true, hasIdempotency: true, hasInbox: true, hasFencing: true}, wantOwed: 1_000},
		// FP08 fires after the second entry, so the rerun books both again:
		// the first settlement now looks like a duplicate of itself, and the
		// partner owes 1000 while suspense says 3000.
		{name: "entry by entry", policy: application.EntryByEntry{}, schema: schema{hasAtomicLedger: true, hasBalanceLocking: true, hasIdempotency: true, hasInbox: true}, wantOwed: 3_000},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useSchema(t, tc.schema)

			p := newOutboxBank(t)
			t.Cleanup(func() { renumberRebookedItems(t, p) })
			statement := newStatement(t, p)
			created := p.sentTransfer(t, 1_000)
			other := p.sentTransfer(t, 300)
			statement.add(application.StatementSettlement, created.EndToEndID, 1_000, "")
			statement.add(application.StatementSettlement, created.EndToEndID, 1_000, "")
			statement.add(application.StatementSettlement, other.EndToEndID, 300, "")

			suspenseBefore := p.systemBalance(t, ledger.SuspenseAccountID())

			armFP08(t)

			err := p.reconciler(statement, tc.policy).RunOnce(t.Context())
			if !errors.Is(err, failpoint.ErrSynthetic) {
				t.Fatalf("the crash did not stop the batch: %v", err)
			}

			failpoint.DisarmAll()

			err = p.reconciler(statement, tc.policy).RunOnce(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			owed := suspenseBefore - p.systemBalance(t, ledger.SuspenseAccountID())
			if owed != tc.wantOwed {
				t.Fatalf("suspense moved by %d, want %d", owed, tc.wantOwed)
			}
		})
	}
}

// renumberRebookedItems moves the items the naive variant booked twice out
// of the way of D10's unique partner_seq, so the robust schema comes back.
func renumberRebookedItems(t *testing.T, p pixBank) {
	t.Helper()

	_, err := p.bank.pool.Exec(context.Background(), `
UPDATE reconciliation_items SET partner_seq = -id
WHERE id NOT IN (SELECT min(id) FROM reconciliation_items GROUP BY partner_seq)`)
	if err != nil {
		t.Error(err)
	}
}

// pausingStatement hands out the statement and, before the reconciler that
// asked can book it, lets its lease expire and another worker take over: a
// reconciler frozen past the TTL (FP07 or FP10 with freeze).
type pausingStatement struct {
	statement *fakeStatement
	meantime  func()
}

func (p pausingStatement) Statement(ctx context.Context, afterSeq int64, limit int) ([]application.StatementEntry, error) {
	entries, err := p.statement.Statement(ctx, afterSeq, limit)
	p.meantime()

	return entries, err
}

func (p pixBank) reconcilerWith(statement application.PartnerStatementReader, lease application.LeasePolicy, owner string) application.Reconciler {
	config := application.ReconcilerConfig{UnitOfWork: p.unitOfWork, Lease: lease, Machine: application.GuardedTransitions{}, Statement: statement, Policy: application.AtomicBatches{}, Owner: owner, Logger: discardLogger()}

	return application.NewReconciler(config)
}

// TestFencing_ZombieReconcilerWriteIsRejected is FC36 and PM-012: the
// reconciler read a batch with a double settlement, paused past its lease,
// and another worker booked the batch. With D11 the zombie's transaction
// sees a stale token and aborts; the naive lease trusts its old check and
// books the double settlement a second time.
func TestFencing_ZombieReconcilerWriteIsRejected(t *testing.T) {
	cases := []struct {
		name     string
		lease    application.LeasePolicy
		schema   schema
		wantOwed int64
	}{
		{name: "fenced", lease: application.FencedLeases{}, schema: schema{hasAtomicLedger: true, hasBalanceLocking: true, hasIdempotency: true, hasInbox: true, hasFencing: true}, wantOwed: 800},
		// The zombie books the whole batch again, after the successor: both
		// settlements now look like duplicates, and suspense holds 2400 for
		// the 800 the partner owes.
		{name: "local clock", lease: application.LocalClockLeases{}, schema: schema{hasAtomicLedger: true, hasBalanceLocking: true, hasIdempotency: true, hasInbox: true}, wantOwed: 2_400},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useSchema(t, tc.schema)

			p := newOutboxBank(t)
			t.Cleanup(func() { renumberRebookedItems(t, p) })
			statement := newStatement(t, p)
			created := p.sentTransfer(t, 800)
			statement.add(application.StatementSettlement, created.EndToEndID, 800, "")
			statement.add(application.StatementSettlement, created.EndToEndID, 800, "")

			suspenseBefore := p.systemBalance(t, ledger.SuspenseAccountID())

			successorTakesOver := func() {
				_, err := p.bank.pool.Exec(t.Context(), `UPDATE worker_leases SET expires_at = now() - interval '1 second' WHERE resource = 'reconciler'`)
				if err != nil {
					t.Error(err)
				}

				err = p.reconcilerWith(statement, tc.lease, "worker-b").RunOnce(t.Context())
				if err != nil {
					t.Error(err)
				}
			}

			zombie := pausingStatement{statement: statement, meantime: sync.OnceFunc(successorTakesOver)}

			// The fenced zombie fails its write; the naive one succeeds.
			_ = p.reconcilerWith(zombie, tc.lease, "worker-a").RunOnce(t.Context())

			owed := suspenseBefore - p.systemBalance(t, ledger.SuspenseAccountID())
			if owed != tc.wantOwed {
				t.Fatalf("suspense moved by %d, want %d", owed, tc.wantOwed)
			}
		})
	}
}

// TestLease_ExpiryUsesDatabaseClock is FC35 and PM-013: a worker whose clock
// runs two minutes ahead thinks a live lease expired. With D11 expiry is
// the database's now(), and the skew changes nothing.
func TestLease_ExpiryUsesDatabaseClock(t *testing.T) {
	cases := []struct {
		name       string
		lease      application.LeasePolicy
		wantStolen bool
	}{
		{name: "fenced", lease: application.FencedLeases{}, wantStolen: false},
		{name: "local clock", lease: application.LocalClockLeases{}, wantStolen: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newOutboxBank(t)
			p.releaseSweeperLease(t)
			t.Cleanup(clock.ClearSkew)

			acquire := func(owner string) bool {
				var isHeld bool

				err := p.unitOfWork.Run(t.Context(), func(ctx context.Context, stores application.Stores) error {
					var err error

					_, isHeld, err = tc.lease.Acquire(ctx, stores, application.LeaseSweeper, owner, application.SweeperLeaseTTL)

					return err
				})
				if err != nil {
					t.Fatal(err)
				}

				return isHeld
			}

			if !acquire("worker-1") {
				t.Fatal("the first worker could not take a free lease")
			}

			err := clock.SetSkew(2*time.Minute, time.Minute, time.Now())
			if err != nil {
				t.Fatal(err)
			}

			isStolen := acquire("worker-2")
			if isStolen != tc.wantStolen {
				t.Fatalf("a worker two minutes ahead took the live lease: %v, want %v", isStolen, tc.wantStolen)
			}
		})
	}
}

// TestReconciliation_FailpointsMatchThePixCorrelation guards FP07, FP08 and
// FP10 as the lab arms them, by the round's prefix: the reconciler has no request
// of its own, so without the PIX's correlation id from the statement they
// never fire, and kill-during-reconciliation attacks nothing.
func TestReconciliation_FailpointsMatchThePixCorrelation(t *testing.T) {
	for _, name := range []failpoint.Name{failpoint.ReconAfterComputeBeforeApply, failpoint.ReconMidApply, failpoint.LeaseAfterCheckBeforeWrite} {
		t.Run(string(name), func(t *testing.T) {
			p := newOutboxBank(t)
			statement := newStatement(t, p)
			created := p.sentTransfer(t, 1_000)
			next := statement.entries[len(statement.entries)-1].Seq + 1
			statement.entries = append(statement.entries, application.StatementEntry{Seq: next, Kind: application.StatementSettlement, EndToEndID: created.EndToEndID, AmountCents: 1_000, CorrelationID: "r0001-p01-000001"})

			failpoint.DisarmAll()
			failpoint.ConfigureEvents(failpoint.EventsConfig{Dir: t.TempDir(), Service: "bank-worker", Instance: "test"})
			t.Cleanup(failpoint.DisarmAll)

			rule := failpoint.Rule{Action: failpoint.ActionError, Arg: "crash", Probability: 1, Match: failpoint.Match{CorrelationPrefix: "r0001-"}, TTL: "1m"}

			err := failpoint.Arm(name, rule, time.Now())
			if err != nil {
				t.Fatal(err)
			}

			err = p.reconciler(statement, application.AtomicBatches{}).RunOnce(t.Context())
			if !errors.Is(err, failpoint.ErrSynthetic) {
				t.Fatalf("%s did not fire for the round's PIX: %v", name, err)
			}
		})
	}
}
