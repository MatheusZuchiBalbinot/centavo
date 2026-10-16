package application

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/correlation"
	"centavo/bank/internal/platform/failpoint"
)

// Sweeper timings (docs/architecture.md, sections 5.7 and 6).
const (
	SweeperInterval = 10 * time.Second
	SweeperLeaseTTL = 30 * time.Second
	// StuckAfter is how long a transfer may sit in created or sent before
	// the sweeper asks the partner about it.
	StuckAfter   = 30 * time.Second
	sweeperBatch = 100
)

// LeaseSweeper is the sweeper's row in worker_leases.
const LeaseSweeper = "sweeper"

// errSweeperLeaseLost stops a pass whose lease moved to another worker.
var errSweeperLeaseLost = errors.New("sweeper lease lost to another worker")

// StuckTransfer is a transfer the saga left in created or sent.
type StuckTransfer struct {
	ID         transfer.ID
	EndToEndID string
	ClientRef  string
}

// LeaseStore keeps the leases of the singleton loops (docs/domain.md, section 13.9).
type LeaseStore interface {
	// AcquireLease takes or renews the lease; the token grows when the lease
	// changes hands. false means another holder has it.
	AcquireLease(ctx context.Context, resource, holder string, ttl time.Duration) (int64, bool, error)
	// HoldsLease checks the lease and keeps it from moving until the
	// transaction ends, so the check and the writes after it are atomic.
	HoldsLease(ctx context.Context, resource, holder string, token int64) (bool, error)
	ListStuck(ctx context.Context, olderThan time.Duration, limit int) ([]StuckTransfer, error)
}

// Sweeper is defense D8 (docs/architecture.md, section 5.7): it asks the
// partner about transfers stuck in created or sent, because their webhook
// never came or was lost, and walks them to the partner's answer. It never
// fails a transfer on its own: only the partner's refusal does that.
type Sweeper struct {
	unitOfWork UnitOfWork
	machine    StateMachine
	partner    PartnerStatusReader
	owner      string
	logger     *slog.Logger
}

// SweeperConfig wires a sweeper.
type SweeperConfig struct {
	UnitOfWork UnitOfWork
	Machine    StateMachine
	Partner    PartnerStatusReader
	Owner      string
	Logger     *slog.Logger
}

// NewSweeper builds the sweeper.
func NewSweeper(config SweeperConfig) Sweeper {
	return Sweeper{unitOfWork: config.UnitOfWork, machine: config.Machine, partner: config.Partner, owner: config.Owner, logger: config.Logger}
}

// Run sweeps until ctx ends.
func (s Sweeper) Run(ctx context.Context) {
	ticker := time.NewTicker(SweeperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		err := s.RunOnce(ctx, StuckAfter)
		if err != nil {
			s.logger.WarnContext(ctx, "sweeping stuck transfers", "error", err)
		}
	}
}

// RunOnce sweeps the transfers stuck for longer than olderThan, if this
// worker holds the lease.
func (s Sweeper) RunOnce(ctx context.Context, olderThan time.Duration) error {
	var (
		token  int64
		isHeld bool
		stuck  []StuckTransfer
	)

	err := s.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		token, isHeld, err = stores.AcquireLease(ctx, LeaseSweeper, s.owner, SweeperLeaseTTL)
		if err != nil || !isHeld {
			return err
		}

		stuck, err = stores.ListStuck(ctx, olderThan, sweeperBatch)

		return err
	})
	if err != nil {
		return err
	}

	for _, candidate := range stuck {
		err := s.sweep(ctx, candidate, token)
		if errors.Is(err, errSweeperLeaseLost) {
			return err
		}

		if err != nil {
			s.logger.WarnContext(ctx, "sweeping a transfer", "transfer_id", candidate.ID.String(), "error", err)
		}
	}

	return nil
}

// sweep asks the partner outside any transaction, then walks the transfer to
// the answer in a transaction that first checks the lease.
func (s Sweeper) sweep(ctx context.Context, candidate StuckTransfer, token int64) error {
	ctx = correlation.With(ctx, candidate.ClientRef)

	authoritative, err := s.partner.Status(ctx, candidate.EndToEndID)
	if err != nil {
		return err
	}

	// FP11: the status is in hand and nothing is written yet; a sweeper
	// that pauses here must not write with a lease it lost.
	err = failpoint.Hit(ctx, failpoint.SweeperAfterStatusBeforeTransition)
	if err != nil {
		return err
	}

	err = s.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		isHeld, err := stores.HoldsLease(ctx, LeaseSweeper, s.owner, token)
		if err != nil {
			return err
		}

		if !isHeld {
			return errSweeperLeaseLost
		}

		walk := pathWalk{transferID: candidate.ID, authoritative: authoritative, cause: causeSweeper, correlationID: candidate.ClientRef}

		return walkPath(ctx, stores, s.machine, walk)
	})
	if isUnresolvable(err) {
		s.logger.ErrorContext(ctx, "bank and partner disagree on a transfer", "transfer_id", candidate.ID.String(), "partner_status", string(authoritative.Status), "error", err)

		return nil
	}

	return err
}
