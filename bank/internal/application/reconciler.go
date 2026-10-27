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
	"centavo/bank/internal/platform/labevent"
)

const (
	ReconcileInterval   = time.Minute
	ReconcilerLeaseTTL  = 30 * time.Second
	reconcileBatchLimit = 500
)

const LeaseReconciler = "reconciler"

const causeReconciliation TransitionCause = "reconciliation"

var (
	errReconcilerLeaseLost = errors.New("reconciler lease lost to another worker")
	errCursorMoved         = errors.New("reconciliation cursor moved under the batch")
)

type ReconciliationItem struct {
	RunID       int64
	PartnerSeq  int64
	EndToEndID  transfer.EndToEndID
	EntryKind   StatementKind
	AmountCents money.Cents
	Divergence  Divergence
	Action      ReconciliationAction
}

type ReconciliationStore interface {
	ReadCursor(ctx context.Context) (int64, error)
	AdvanceCursor(ctx context.Context, from, to int64) (bool, error)
	SaveCursor(ctx context.Context, to int64) error
	StartRun(ctx context.Context, from, to, token int64) (int64, error)
	FinishRun(ctx context.Context, runID int64) error
	InsertItem(ctx context.Context, item ReconciliationItem) (int64, error)
	CountItems(ctx context.Context, endToEndID transfer.EndToEndID, kind StatementKind) (int, error)
	IsIncomingCredited(ctx context.Context, endToEndID transfer.EndToEndID) (bool, error)
}

type PartnerStatementReader interface {
	Statement(ctx context.Context, afterSeq int64, limit int) ([]StatementEntry, error)
}

type BatchPolicy interface {
	Book(ctx context.Context, r Reconciler, batch statementBatch) error
}

type Reconciler struct {
	unitOfWork UnitOfWork
	lease      LeasePolicy
	machine    StateMachine
	statement  PartnerStatementReader
	policy     BatchPolicy
	owner      string
	logger     *slog.Logger
}

type ReconcilerConfig struct {
	UnitOfWork UnitOfWork
	Lease      LeasePolicy
	Machine    StateMachine
	Statement  PartnerStatementReader
	Policy     BatchPolicy
	Owner      string
	Logger     *slog.Logger
}

// AtomicBatches is the reconciliation defense: the run, its items, their
// actions and the cursor fall in one commit, behind the lease. A death in the
// middle undoes the batch, and the next run starts again from the cursor.
type AtomicBatches struct{}

// EntryByEntry is the naive variant: each entry commits on its own and the
// cursor is saved after the batch. A death in the middle leaves the first
// entries booked and the cursor behind them, so the next run books them again.
type EntryByEntry struct{}

type actStores interface {
	TransitionStores
	creditStores
}

type entryStores interface {
	actStores
	factStores
}

type factStores interface {
	PixStore
	PositionStore
	ReconciliationStore
}

type statementBatch struct {
	from    int64
	to      int64
	token   int64
	entries []StatementEntry
}

type actCommand struct {
	entry    StatementEntry
	facts    entryFacts
	decision verdict
	itemID   int64
}

func NewReconciler(config ReconcilerConfig) Reconciler {
	return Reconciler{
		unitOfWork: config.UnitOfWork, lease: config.Lease, machine: config.Machine, statement: config.Statement,
		policy: config.Policy, owner: config.Owner, logger: config.Logger,
	}
}

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

func (r Reconciler) RunOnce(ctx context.Context) error {
	for ctx.Err() == nil {
		batch, isHeld, err := r.nextBatch(ctx)
		if err != nil {
			return err
		}

		hasNothingToBook := !isHeld || len(batch.entries) == 0
		if hasNothingToBook {
			return nil
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

func (AtomicBatches) Book(ctx context.Context, r Reconciler, batch statementBatch) error {
	corrected := 0

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		corrected = 0

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
			isCorrection, err := r.bookEntry(ctx, stores, runID, entry)
			if err != nil {
				return err
			}

			if isCorrection {
				corrected++
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

	// Told only after the commit: a rolled back batch corrected nothing.
	if err == nil && corrected > 0 {
		labevent.Record(ctx, labevent.Reconciled, LeaseReconciler, corrected)
	}

	return err
}

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
		isCorrection := false

		err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
			var err error

			isCorrection, err = r.bookEntry(ctx, stores, runID, entry)

			return err
		})
		if err != nil {
			return err
		}

		if isCorrection {
			labevent.Record(ctx, labevent.Reconciled, LeaseReconciler, 1)
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

func (r Reconciler) nextBatch(ctx context.Context) (statementBatch, bool, error) {
	var (
		batch  statementBatch
		isHeld bool
	)

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		batch.token, isHeld, err = r.lease.Acquire(ctx, stores, Lease{Resource: LeaseReconciler, Holder: r.owner}, ReconcilerLeaseTTL)
		if err != nil {
			return err
		}

		if !isHeld {
			return nil
		}

		batch.from, err = stores.ReadCursor(ctx)

		return err
	})
	if err != nil {
		return statementBatch{}, isHeld, err
	}

	if !isHeld {
		return statementBatch{}, isHeld, nil
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

// Reports whether the entry corrected something the bank had missed.
func (r Reconciler) bookEntry(ctx context.Context, stores entryStores, runID int64, entry StatementEntry) (bool, error) {
	ctx = correlation.With(ctx, entryCorrelation(entry))

	facts, err := factsOf(ctx, stores, entry)
	if err != nil {
		return false, err
	}

	decision, err := classify(entry, facts)
	if err != nil {
		return false, err
	}

	item := ReconciliationItem{
		RunID: runID, PartnerSeq: entry.Seq, EndToEndID: entry.EndToEndID, EntryKind: entry.Kind,
		AmountCents: entry.AmountCents, Divergence: decision.divergence, Action: decision.action,
	}

	itemID, err := stores.InsertItem(ctx, item)
	if err != nil {
		return false, err
	}

	err = r.act(ctx, stores, actCommand{entry: entry, facts: facts, decision: decision, itemID: itemID})

	return decision.divergence != DivergenceMatched, err
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

// The partner reserve mirror moves by what the partner did, and suspense
// holds the other side.
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

// The lab arms failpoints for the round's correlation ids, and the
// reconciler has no request of its own to take one from.
func entryFailpointContext(ctx context.Context, entry StatementEntry) context.Context {
	if entry.CorrelationID == "" {
		return ctx
	}

	return correlation.With(ctx, entry.CorrelationID)
}

// Takes the first entry of the batch that has a correlation id.
func batchFailpointContext(ctx context.Context, batch statementBatch) context.Context {
	for _, entry := range batch.entries {
		if entry.CorrelationID != "" {
			return entryFailpointContext(ctx, entry)
		}
	}

	return ctx
}

func entryCorrelation(entry StatementEntry) string {
	return fmt.Sprintf("recon-%d", entry.Seq)
}
