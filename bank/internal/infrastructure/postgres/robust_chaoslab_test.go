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
	"centavo/bank/internal/infrastructure/postgres"
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
	intake, processor := newInbox(p)
	drainInbox(t, processor)

	account := p.bank.newCustomer(t)
	p.bank.fund(t, account, 10_000)
	created := p.createPixOut(t, account, 4_500)
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
