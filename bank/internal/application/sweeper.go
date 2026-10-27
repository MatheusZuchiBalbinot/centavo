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

const (
	SweeperInterval = 10 * time.Second
	SweeperLeaseTTL = 30 * time.Second
	StuckAfter      = 30 * time.Second
	sweeperBatch    = 100
)

const LeaseSweeper = "sweeper"

var errSweeperLeaseLost = errors.New("sweeper lease lost to another worker")

type StuckTransfer struct {
	ID         transfer.ID
	EndToEndID transfer.EndToEndID
	ClientRef  string
}

type LeaseStore interface {
	// The token grows when the lease changes hands.
	AcquireLease(ctx context.Context, lease Lease, ttl time.Duration) (int64, bool, error)
	// AcquireLeaseAt is the naive acquisition: expiry computed from the
	// caller's own clock.
	AcquireLeaseAt(ctx context.Context, lease Lease, now time.Time, ttl time.Duration) (int64, bool, error)
	// Keeps the lease from moving until the transaction ends, so the check and
	// the writes after it are atomic.
	HoldsLease(ctx context.Context, lease Lease, token int64) (bool, error)
	ListStuck(ctx context.Context, olderThan time.Duration, limit int) ([]StuckTransfer, error)
}

// Sweeper is the recovery defense: it asks the partner about transfers stuck in
// created or sent, because their webhook never came or was lost, and walks them
// to the partner's answer. It never fails a transfer on its own: only the
// partner's refusal does that.
type Sweeper struct {
	unitOfWork UnitOfWork
	lease      LeasePolicy
	machine    StateMachine
	partner    PartnerStatusReader
	owner      string
	logger     *slog.Logger
}

type SweeperConfig struct {
	UnitOfWork UnitOfWork
	Lease      LeasePolicy
	Machine    StateMachine
	Partner    PartnerStatusReader
	Owner      string
	Logger     *slog.Logger
}

func NewSweeper(config SweeperConfig) Sweeper {
	return Sweeper{
		unitOfWork: config.UnitOfWork, lease: config.Lease, machine: config.Machine, partner: config.Partner, owner: config.Owner, logger: config.Logger,
	}
}

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

func (s Sweeper) RunOnce(ctx context.Context, olderThan time.Duration) error {
	var (
		token  int64
		isHeld bool
		stuck  []StuckTransfer
	)

	err := s.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		token, isHeld, err = s.lease.Acquire(ctx, stores, Lease{Resource: LeaseSweeper, Holder: s.owner}, SweeperLeaseTTL)
		if err != nil {
			return err
		}

		if !isHeld {
			return nil
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

// Asks the partner outside any transaction, then walks the transfer to the
// answer in a transaction that first checks the lease.
func (s Sweeper) sweep(ctx context.Context, candidate StuckTransfer, token int64) error {
	ctx = correlation.With(ctx, candidate.ClientRef)

	authoritative, err := s.partner.Status(ctx, candidate.EndToEndID)
	if err != nil {
		return err
	}

	// `sweeper.after_status_before_transition`: the status is in hand and
	// nothing is written yet; a sweeper that pauses here must not write with a
	// lease it lost.
	err = failpoint.Hit(ctx, failpoint.SweeperAfterStatusBeforeTransition)
	if err != nil {
		return err
	}

	err = failpoint.Hit(ctx, failpoint.LeaseAfterCheckBeforeWrite)
	if err != nil {
		return err
	}

	err = s.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		isHeld, err := s.lease.Holds(ctx, stores, Lease{Resource: LeaseSweeper, Holder: s.owner}, token)
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
		s.logger.ErrorContext(
			ctx,
			"bank and partner disagree on a transfer",
			"transfer_id",
			candidate.ID.String(),
			"partner_status",
			string(authoritative.Status),
			"error",
			err,
		)

		return nil
	}

	return err
}
