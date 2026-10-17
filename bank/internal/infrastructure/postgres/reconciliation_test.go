package postgres_test

import (
	"context"
	"testing"

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

func (f *fakeStatement) Statement(_ context.Context, afterSeq int64, limit int) ([]application.StatementEntry, error) {
	var page []application.StatementEntry

	for _, entry := range f.entries {
		if entry.Seq > afterSeq && len(page) < limit {
			page = append(page, entry)
		}
	}

	return page, nil
}

// add appends an entry after the last one.
func (f *fakeStatement) add(kind application.StatementKind, endToEndID string, cents money.Cents, accountID string) {
	seq := f.entries[len(f.entries)-1].Seq + 1
	f.entries = append(f.entries, application.StatementEntry{Seq: seq, Kind: kind, EndToEndID: endToEndID, AmountCents: cents, AccountID: accountID})
}

// newStatement moves the cursor past everything reconciled so far and
// starts the test's statement right after it.
func newStatement(t *testing.T, p pixBank) *fakeStatement {
	t.Helper()

	var base int64

	err := p.bank.pool.QueryRow(t.Context(), `SELECT greatest(coalesce(max(partner_seq), 0), (SELECT last_partner_seq FROM reconciliation_cursor)) + $1 FROM reconciliation_items`, seqGap).Scan(&base)
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.bank.pool.Exec(t.Context(), `UPDATE reconciliation_cursor SET last_partner_seq = $1`, base)
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.bank.pool.Exec(t.Context(), `UPDATE worker_leases SET holder = NULL, expires_at = NULL WHERE resource = 'reconciler'`)
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

// reconcile runs the robust reconciler (D10) once.
func (p pixBank) reconcile(t *testing.T, statement application.PartnerStatementReader) {
	t.Helper()

	err := p.reconciler(statement, application.AtomicBatches{}).RunOnce(t.Context())
	if err != nil {
		t.Fatal(err)
	}
}

// TestReconciliation_MissingSettlement_ConfirmsTheTransfer: the webhook was
// lost and the sweeper has not run; the statement shows the settlement.
func TestReconciliation_MissingSettlement_ConfirmsTheTransfer(t *testing.T) {
	p := newOutboxBank(t)
	statement := newStatement(t, p)
	created := p.sentTransfer(t, 2_200)
	statement.add(application.StatementSettlement, created.EndToEndID, 2_200, "")

	p.reconcile(t, statement)

	view := p.status(t, created.TransferID)
	if view.Transfer.Status != transfer.StatusConfirmed || p.settlements(t, created.TransferID, "pix_out_settle") != 1 {
		t.Fatalf("status %s after reconciling a settlement", view.Transfer.Status)
	}
}

// TestReconciliation_PartnerDoubleSettlement_BooksSuspense is FC34: the
// partner settled the same PIX twice. The second settlement left the
// reserve too, and suspense holds what the partner owes back.
func TestReconciliation_PartnerDoubleSettlement_BooksSuspense(t *testing.T) {
	p := newOutboxBank(t)
	statement := newStatement(t, p)
	created := p.sentTransfer(t, 1_500)
	statement.add(application.StatementSettlement, created.EndToEndID, 1_500, "")
	statement.add(application.StatementSettlement, created.EndToEndID, 1_500, "")

	suspenseBefore := p.systemBalance(t, ledger.SuspenseAccountID())
	p.reconcile(t, statement)

	owed := suspenseBefore - p.systemBalance(t, ledger.SuspenseAccountID())
	if owed != 1_500 || p.settlements(t, created.TransferID, "pix_out_settle") != 1 {
		t.Fatalf("suspense moved by %d, %d settlements", owed, p.settlements(t, created.TransferID, "pix_out_settle"))
	}
}

// TestReconciliation_MissingIncoming_CreditsOnce: a PIX in whose webhook
// never came is credited by the reconciliation, once, even if run again.
func TestReconciliation_MissingIncoming_CreditsOnce(t *testing.T) {
	p := newOutboxBank(t)
	statement := newStatement(t, p)
	account := p.bank.newCustomer(t)
	statement.add(application.StatementIncoming, "E"+uuid.NewString()[:8]+"0000cccc", 640, account.String())

	p.reconcile(t, statement)
	p.reconcile(t, statement)

	assertChainedAndNeverNegative(t, p.bank, account, 640)
}
