package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

// seqGap keeps each test's statement apart from the entries earlier tests
// reconciled in the shared database.
const seqGap = 1_000

// fakeStatement is a partner statement fixed by the test.
type fakeStatement struct {
	entries []application.StatementEntry
}

// TestReconciliation_MissingSettlement_ConfirmsTheTransfer: the webhook was
// lost and the sweeper has not run; the statement shows the settlement.
func TestReconciliation_MissingSettlement_ConfirmsTheTransfer(t *testing.T) {
	fixture := newOutboxBank(t)
	statement := newStatement(t, fixture)
	created := fixture.sentTransfer(t, 2_200)
	statement.add(application.StatementSettlement, created.EndToEndID, 2_200, "")

	fixture.reconcile(t, statement)

	view := fixture.status(t, created.TransferID)
	isUnexpected := view.Transfer.Status != transfer.StatusConfirmed || fixture.settlements(t, created.TransferID, "pix_out_settle") != 1
	if isUnexpected {
		t.Fatalf("status %s after reconciling a settlement", view.Transfer.Status)
	}
}

// TestReconciliation_PartnerDoubleSettlement_BooksSuspense is a regression
// test: the partner settled the same PIX twice. The second settlement left the
// reserve too, and suspense holds what the partner owes back.
func TestReconciliation_PartnerDoubleSettlement_BooksSuspense(t *testing.T) {
	fixture := newOutboxBank(t)
	statement := newStatement(t, fixture)
	created := fixture.sentTransfer(t, 1_500)
	statement.add(application.StatementSettlement, created.EndToEndID, 1_500, "")
	statement.add(application.StatementSettlement, created.EndToEndID, 1_500, "")

	suspenseBefore := fixture.systemBalance(t, ledger.SuspenseAccountID())
	fixture.reconcile(t, statement)

	owed := suspenseBefore - fixture.systemBalance(t, ledger.SuspenseAccountID())
	isUnexpected := owed != 1_500 || fixture.settlements(t, created.TransferID, "pix_out_settle") != 1
	if isUnexpected {
		t.Fatalf("suspense moved by %d, %d settlements", owed, fixture.settlements(t, created.TransferID, "pix_out_settle"))
	}
}

// TestReconciliation_MissingIncoming_CreditsOnce: a PIX in whose webhook
// never came is credited by the reconciliation, once, even if run again.
func TestReconciliation_MissingIncoming_CreditsOnce(t *testing.T) {
	fixture := newOutboxBank(t)
	statement := newStatement(t, fixture)
	account := fixture.bank.newCustomer(t)
	statement.add(application.StatementIncoming, transfer.EndToEndID("E"+uuid.NewString()[:8]+"0000cccc"), 640, account.String())

	fixture.reconcile(t, statement)
	fixture.reconcile(t, statement)

	assertChainedAndNeverNegative(t, fixture.bank, account, 640)
}

// TestReconciliation_BatchTakesItsTransfersBeforeAnyBalance reproduces the
// deadlock the chaos rounds hit about once a minute: a writer holds a
// transfer of the batch and then needs the transit balance. A reconciler that
// booked an earlier entry first holds that balance while it waits on the
// transfer; one that takes the batch's transfers first waits holding none.
func TestReconciliation_BatchTakesItsTransfersBeforeAnyBalance(t *testing.T) {
	fixture := newOutboxBank(t)
	statement := newStatement(t, fixture)
	first := fixture.sentTransfer(t, 1_000)
	second := fixture.sentTransfer(t, 300)
	statement.add(application.StatementSettlement, first.EndToEndID, 1_000, "")
	statement.add(application.StatementSettlement, second.EndToEndID, 300, "")

	writer, err := fixture.bank.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	defer writer.Rollback(context.Background()) //nolint:errcheck // a no-op after the commit below

	_, err = writer.Exec(t.Context(), `SELECT id FROM external_transfers WHERE id = $1 FOR UPDATE`, uuid.UUID(second.TransferID))
	if err != nil {
		t.Fatal(err)
	}

	reconciled := make(chan error, 1)

	go func() {
		reconciled <- fixture.reconciler(statement, application.AtomicBatches{}).RunOnce(context.Background())
	}()

	waitForALockWait(t, fixture)

	_, err = writer.Exec(t.Context(), `UPDATE account_balances SET updated_at = now() WHERE account_id = $1`, uuid.UUID(ledger.PixOutTransitAccountID()))
	if err != nil {
		t.Fatalf("the other writer could not take the transit balance: %v", err)
	}

	err = writer.Commit(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	err = <-reconciled
	if err != nil {
		t.Fatalf("the reconciler failed: %v", err)
	}
}

func (f *fakeStatement) Statement(_ context.Context, afterSeq int64, limit int) ([]application.StatementEntry, error) {
	var page []application.StatementEntry

	for _, entry := range f.entries {
		isOnPage := entry.Seq > afterSeq && len(page) < limit
		if isOnPage {
			page = append(page, entry)
		}
	}

	return page, nil
}

// add appends an entry after the last one.
func (f *fakeStatement) add(kind application.StatementKind, endToEndID transfer.EndToEndID, cents money.Cents, accountID string) {
	seq := f.entries[len(f.entries)-1].Seq + 1
	f.entries = append(f.entries, application.StatementEntry{Seq: seq, Kind: kind, EndToEndID: endToEndID, AmountCents: cents, AccountID: accountID})
}

// newStatement moves the cursor past everything reconciled so far and
// starts the test's statement right after it.
func newStatement(t *testing.T, fixture pixBank) *fakeStatement {
	t.Helper()

	var base int64

	err := fixture.bank.pool.QueryRow(t.Context(), `SELECT greatest(coalesce(max(partner_seq), 0), (SELECT last_partner_seq FROM reconciliation_cursor)) + $1 FROM reconciliation_items`, seqGap).Scan(&base)
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.bank.pool.Exec(t.Context(), `UPDATE reconciliation_cursor SET last_partner_seq = $1`, base)
	if err != nil {
		t.Fatal(err)
	}

	_, err = fixture.bank.pool.Exec(t.Context(), `UPDATE worker_leases SET holder = NULL, expires_at = NULL WHERE resource = 'reconciler'`)
	if err != nil {
		t.Fatal(err)
	}

	// A sentinel the reconciler never sees: the statement starts after it.
	return &fakeStatement{entries: []application.StatementEntry{{Seq: base}}}
}

func (p pixBank) reconciler(statement application.PartnerStatementReader, policy application.BatchPolicy) application.Reconciler {
	config := application.ReconcilerConfig{UnitOfWork: p.unitOfWork, Lease: application.FencedLeases{}, Machine: application.GuardedTransitions{}, Statement: statement, Policy: policy, Owner: "worker-a", Logger: discardLogger()}

	return application.NewReconciler(config)
}

func (p pixBank) systemBalance(t *testing.T, id ledger.AccountID) int64 {
	t.Helper()

	var balance int64

	err := p.bank.pool.QueryRow(t.Context(), `SELECT coalesce(sum(amount_cents), 0) FROM ledger_entries WHERE account_id = $1`, uuid.UUID(id)).Scan(&balance)
	if err != nil {
		t.Fatal(err)
	}

	return balance
}

// reconcile runs the robust reconciler (reconciliation) once.
func (p pixBank) reconcile(t *testing.T, statement application.PartnerStatementReader) {
	t.Helper()

	err := p.reconciler(statement, application.AtomicBatches{}).RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
}

// waitForALockWait returns once some session waits on a lock: the reconciler
// reached the transfer the test holds.
func waitForALockWait(t *testing.T, fixture pixBank) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		var waiting int

		err := fixture.bank.pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}

		if waiting > 0 {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("the reconciler never waited on the held transfer")
}
