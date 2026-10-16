//go:build chaoslab

package naive

import (
	"context"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/infrastructure/postgres"
	"centavo/bank/internal/platform/defenses"
)

func init() {
	defenses.RegisterNaive(defenses.AtomicLedger, func(deps defenses.Dependencies) any {
		return application.UnitOfWork(postgres.NewAutocommit(deps.Pool))
	})

	defenses.RegisterNaive(defenses.BalanceLocking, func(defenses.Dependencies) any {
		return application.Locker(NoLocker{})
	})
}

// NoLocker is the naive variant of D2: it reads balances without locking
// them, so a read and the write that follows can interleave with another
// writer's (FC41).
type NoLocker struct{}

// Lock does nothing.
func (NoLocker) Lock(context.Context, application.Stores, []ledger.AccountID) error {
	return nil
}
