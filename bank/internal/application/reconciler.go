package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/correlation"
	"centavo/bank/internal/platform/failpoint"
)

// Reconciler timings.
const (
	ReconcileInterval   = time.Minute
	ReconcilerLeaseTTL  = 30 * time.Second
	reconcileBatchLimit = 500
)

// actStores are what acting on a classified entry writes: a walk to the
// partner's status, a credit or a suspense posting.
type actStores interface {
	TransitionStores
	creditStores
}

// entryStores are what booking one statement entry reads and writes.
type entryStores interface {
	actStores
	factStores
}

// factStores are what classifying a statement entry reads: the ledger's
// transfers and incoming PIX, balances and earlier items.
type factStores interface {
	PixStore
	PositionStore
	ReconciliationStore
}

// LeaseReconciler is the reconciler's row in worker_leases.
const LeaseReconciler = "reconciler"

// causeReconciliation marks the transitions a reconciliation walks.
const causeReconciliation TransitionCause = "reconciliation"

var (
	errReconcilerLeaseLost = errors.New("reconciler lease lost to another worker")
	// errCursorMoved means another reconciler booked the batch first.
	errCursorMoved = errors.New("reconciliation cursor moved under the batch")
)

// ReconciliationItem is one statement entry as reconciled.
type ReconciliationItem struct {
	RunID       int64
	PartnerSeq  int64
	EndToEndID  transfer.EndToEndID
	EntryKind   StatementKind
	AmountCents money.Cents
	Divergence  Divergence
	Action      ReconciliationAction
}

// ReconciliationStore keeps the cursor, the runs and the items.
type ReconciliationStore interface {
	ReadCursor(ctx context.Context) (int64, error)
	// AdvanceCursor is the compare-and-set.
	AdvanceCursor(ctx context.Context, from, to int64) (bool, error)
	// SaveCursor is the naive write, with no check of where it was.
	SaveCursor(ctx context.Context, to int64) error
	StartRun(ctx context.Context, from, to, token int64) (int64, error)
	FinishRun(ctx context.Context, runID int64) error
	InsertItem(ctx context.Context, item ReconciliationItem) (int64, error)
	// CountItems counts the entries of a kind already reconciled for an
	// end_to_end_id.
	CountItems(ctx context.Context, endToEndID transfer.EndToEndID, kind StatementKind) (int, error)
	IsIncomingCredited(ctx context.Context, endToEndID transfer.EndToEndID) (bool, error)
}

// PartnerStatementReader reads the partner's statement after a sequence number.
type PartnerStatementReader interface {
	Statement(ctx context.Context, afterSeq int64, limit int) ([]StatementEntry, error)
}

// statementBatch is a contiguous slice of the statement.
type statementBatch struct {
	from    int64
	to      int64
	token   int64
	entries []StatementEntry
}

// BatchPolicy is the reconciliation defense's strategy: how a batch is booked.
type BatchPolicy interface {
	Book(ctx context.Context, r Reconciler, batch statementBatch) error
}

// Reconciler is the reconciliation: it reads the partner's statement from a
// cursor and books every entry against the ledger.
type Reconciler struct {
	unitOfWork UnitOfWork
	lease      LeasePolicy
	machine    StateMachine
	statement  PartnerStatementReader
	policy     BatchPolicy
	owner      string
	logger     *slog.Logger
}

// ReconcilerConfig wires a reconciler.
type ReconcilerConfig struct {
	UnitOfWork UnitOfWork
	Lease      LeasePolicy
	Machine    StateMachine
	Statement  PartnerStatementReader
	Policy     BatchPolicy
	Owner      string
	Logger     *slog.Logger
}

// NewReconciler builds the reconciler.
func NewReconciler(config ReconcilerConfig) Reconciler {
	return Reconciler{
		unitOfWork: config.UnitOfWork, lease: config.Lease, machine: config.Machine, statement: config.Statement,
		policy: config.Policy, owner: config.Owner, logger: config.Logger,
	}
}

// Run reconciles until ctx ends.
func (r Reconciler) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		err := r.RunOnce(ctx)
		if err != nil {
			r.logger.WarnContext(ctx, "reconciling with the partner", "error", err)
		}
	}
}

// RunOnce books every statement entry after the cursor, a batch at a time,
// if this worker holds the lease.
func (r Reconciler) RunOnce(ctx context.Context) error {
	for ctx.Err() == nil {
		batch, isHeld, err := r.nextBatch(ctx)
		if err != nil || !isHeld || len(batch.entries) == 0 {
			return err
		}

		// `recon.after_compute_before_apply`: the batch is read and nothing is
		// booked yet.
		err = failpoint.Hit(batchFailpointContext(ctx, batch), failpoint.ReconAfterComputeBeforeApply)
		if err != nil {
			return err
		}

		// `lease.after_check_before_write`: the lease was checked when the
		// batch was read; a worker paused here past the TTL comes back as a
		// zombie.
		err = failpoint.Hit(batchFailpointContext(ctx, batch), failpoint.LeaseAfterCheckBeforeWrite)
		if err != nil {
			return err
		}

		err = r.policy.Book(ctx, r, batch)
		if err != nil {
			return err
		}
	}

	return ctx.Err()
}

func (r Reconciler) nextBatch(ctx context.Context) (statementBatch, bool, error) {
	var (
		batch  statementBatch
		isHeld bool
	)

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		batch.token, isHeld, err = r.lease.Acquire(ctx, stores, Lease{Resource: LeaseReconciler, Holder: r.owner}, ReconcilerLeaseTTL)
		if err != nil || !isHeld {
			return err
		}

		batch.from, err = stores.ReadCursor(ctx)

		return err
	})
	if err != nil || !isHeld {
		return statementBatch{}, isHeld, err
	}

	batch.entries, err = r.statement.Statement(ctx, batch.from, reconcileBatchLimit)
	if err != nil {
		return statementBatch{}, true, err
	}

	if len(batch.entries) > 0 {
		batch.to = batch.entries[len(batch.entries)-1].Seq
	}

	return batch, true, nil
}

// bookEntry classifies one entry and books it, in the caller's unit of work.
func (r Reconciler) bookEntry(ctx context.Context, stores entryStores, runID int64, entry StatementEntry) error {
	ctx = correlation.With(ctx, entryCorrelation(entry))

	facts, err := factsOf(ctx, stores, entry)
	if err != nil {
		return err
	}

	decision, err := classify(entry, facts)
	if err != nil {
		return err
	}

	item := ReconciliationItem{
		RunID: runID, PartnerSeq: entry.Seq, EndToEndID: entry.EndToEndID, EntryKind: entry.Kind,
		AmountCents: entry.AmountCents, Divergence: decision.divergence, Action: decision.action,
	}

	itemID, err := stores.InsertItem(ctx, item)
	if err != nil {
		return err
	}

	return r.act(ctx, stores, actCommand{entry: entry, facts: facts, decision: decision, itemID: itemID})
}

type actCommand struct {
	entry    StatementEntry
	facts    entryFacts
	decision verdict
	itemID   int64
}

func (r Reconciler) act(ctx context.Context, stores actStores, command actCommand) error {
	if command.decision.walkTo != "" {
		walk := pathWalk{
			transferID: command.facts.transfer.ID, authoritative: AuthoritativeStatus{Status: command.decision.walkTo},
			cause: causeReconciliation, correlationID: entryCorrelation(command.entry),
		}

		err := walkPath(ctx, stores, r.machine, walk)
		if isUnresolvable(err) {
			r.logger.ErrorContext(ctx, "reconciliation found a conflict", "end_to_end_id", command.entry.EndToEndID, "error", err)

			err = nil
		}

		if err != nil {
			return err
		}
	}

	if command.decision.action == actionCredit {
		incoming := PartnerEvent{
			ID: entryCorrelation(command.entry), Type: EventIncoming, EndToEndID: command.entry.EndToEndID,
			AmountCents: command.entry.AmountCents, AccountID: command.entry.AccountID,
		}

		return creditIncoming(ctx, stores, incoming)
	}

	if command.decision.suspenseCents == 0 {
		return nil
	}

	return bookSuspense(ctx, stores, command.itemID, command.decision.suspenseCents)
}

// bookSuspense posts a reconciliation adjustment: the partner reserve mirror
// moves by what the partner did, and suspense holds the other side.
func bookSuspense(ctx context.Context, stores PostingStores, itemID int64, cents money.Cents) error {
	transactionID, err := ledger.NewTransactionID()
	if err != nil {
		return err
	}

	transaction := ledger.Transaction{
		ID: transactionID, Kind: ledger.TransactionKindAdjustment, CorrelationID: correlation.From(ctx), ReconciliationItemID: &itemID,
	}

	_, err = stores.InsertTransaction(ctx, transaction)
	if err != nil {
		return err
	}

	postings := []posting{{account: ledger.PartnerSettlementAccountID(), delta: cents}, {account: ledger.SuspenseAccountID(), delta: -cents}}

	_, err = poster{positions: stores, entries: stores}.postAll(ctx, transactionID, postings)

	return err
}

// factsOf reads what the ledger knows about an entry.
func factsOf(ctx context.Context, stores factStores, entry StatementEntry) (entryFacts, error) {
	var facts entryFacts

	prior, err := stores.CountItems(ctx, entry.EndToEndID, entry.Kind)
	if err != nil {
		return entryFacts{}, err
	}

	facts.priorItems = prior

	if entry.Kind == StatementIncoming {
		return incomingFacts(ctx, stores, entry, facts)
	}

	external, err := stores.FindExternalTransferByEndToEnd(ctx, entry.EndToEndID)
	if errors.Is(err, transfer.ErrTransferNotFound) {
		return facts, nil
	}

	if err != nil {
		return entryFacts{}, err
	}

	facts.transfer = external
	facts.hasTransfer = true

	return facts, nil
}

func incomingFacts(ctx context.Context, stores factStores, entry StatementEntry, facts entryFacts) (entryFacts, error) {
	isCredited, err := stores.IsIncomingCredited(ctx, entry.EndToEndID)
	if err != nil {
		return entryFacts{}, err
	}

	facts.isCredited = isCredited

	accountID, err := ledger.ParseAccountID(entry.AccountID)
	if err != nil {
		// A destination the bank cannot even parse is an account it does not
		// have: the entry goes to suspense like any unknown destination (an
		// unexplained divergence goes to suspense, never to a customer).
		return facts, nil //nolint:nilerr // an unparsable account is an unknown one, not a failure
	}

	_, err = stores.ReadCustomerPosition(ctx, accountID)
	if errors.Is(err, ledger.ErrAccountNotFound) {
		return facts, nil
	}

	if err != nil {
		return entryFacts{}, err
	}

	facts.isAccountKnown = true

	return facts, nil
}

// lockBatch takes every PIX of the batch, then its transfers, before the
// first balance. The batch holds hot system balances from its first entry to
// its commit; taking a transfer after them would wait on a writer that holds
// the transfer and waits on those balances, a deadlock every few runs.
func lockBatch(ctx context.Context, stores PixStore, batch statementBatch) error {
	endToEndIDs := make([]transfer.EndToEndID, 0, len(batch.entries))
	for _, entry := range batch.entries {
		endToEndIDs = append(endToEndIDs, entry.EndToEndID)
	}

	slices.Sort(endToEndIDs)
	endToEndIDs = slices.Compact(endToEndIDs)

	for _, endToEndID := range endToEndIDs {
		err := stores.LockEndToEnd(ctx, endToEndID)
		if err != nil {
			return err
		}
	}

	return stores.LockTransfersOf(ctx, endToEndIDs)
}

// entryFailpointContext carries the entry's PIX correlation id to a
// failpoint: the lab arms them for the round's correlation ids, and the
// reconciler has no request of its own to take one from.
func entryFailpointContext(ctx context.Context, entry StatementEntry) context.Context {
	if entry.CorrelationID == "" {
		return ctx
	}

	return correlation.With(ctx, entry.CorrelationID)
}

// batchFailpointContext is entryFailpointContext for a step that covers the
// whole batch: it takes the first entry that has a correlation id.
func batchFailpointContext(ctx context.Context, batch statementBatch) context.Context {
	for _, entry := range batch.entries {
		if entry.CorrelationID != "" {
			return entryFailpointContext(ctx, entry)
		}
	}

	return ctx
}

// entryCorrelation ties the ledger rows a reconciliation writes to the entry.
func entryCorrelation(entry StatementEntry) string {
	return fmt.Sprintf("recon-%d", entry.Seq)
}

// AtomicBatches is the reconciliation defense: the run, its items, their
// actions and the cursor fall in one commit, behind the lease. A death in the
// middle undoes the batch, and the next run starts again from the cursor.
type AtomicBatches struct{}

// Book books the whole batch or nothing.
func (AtomicBatches) Book(ctx context.Context, r Reconciler, batch statementBatch) error {
	return r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		isHeld, err := r.lease.Holds(ctx, stores, Lease{Resource: LeaseReconciler, Holder: r.owner}, batch.token)
		if err != nil {
			return err
		}

		if !isHeld {
			return errReconcilerLeaseLost
		}

		err = lockBatch(ctx, stores, batch)
		if err != nil {
			return err
		}

		runID, err := stores.StartRun(ctx, batch.from, batch.to, batch.token)
		if err != nil {
			return err
		}

		for _, entry := range batch.entries {
			err := r.bookEntry(ctx, stores, runID, entry)
			if err != nil {
				return err
			}

			// `recon.mid_apply`: between two entries of the batch.
			err = failpoint.Hit(entryFailpointContext(ctx, entry), failpoint.ReconMidApply)
			if err != nil {
				return err
			}
		}

		isAdvanced, err := r.lease.MoveCursor(ctx, stores, batch.from, batch.to)
		if err != nil {
			return err
		}

		if !isAdvanced {
			return errCursorMoved
		}

		return stores.FinishRun(ctx, runID)
	})
}

// EntryByEntry is the naive variant: each entry commits on its own and the
// cursor is saved after the batch. A death in the middle leaves the first
// entries booked and the cursor behind them, so the next run books them again.
type EntryByEntry struct{}

// Book books the batch an entry at a time.
func (EntryByEntry) Book(ctx context.Context, r Reconciler, batch statementBatch) error {
	var runID int64

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		runID, err = stores.StartRun(ctx, batch.from, batch.to, batch.token)

		return err
	})
	if err != nil {
		return err
	}

	for _, entry := range batch.entries {
		err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
			return r.bookEntry(ctx, stores, runID, entry)
		})
		if err != nil {
			return err
		}

		err = failpoint.Hit(entryFailpointContext(ctx, entry), failpoint.ReconMidApply)
		if err != nil {
			return err
		}
	}

	return r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		err := stores.SaveCursor(ctx, batch.to)
		if err != nil {
			return err
		}

		return stores.FinishRun(ctx, runID)
	})
}
