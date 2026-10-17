//go:build chaoslab

package naive

import (
	"context"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
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

	defenses.RegisterNaive(defenses.Idempotency, func(deps defenses.Dependencies) any {
		resolver := ClientRefResolver{store: postgres.NewLedgerStore(deps.Pool)}

		return application.IdempotencyStrategy{Keys: IgnoredKeys{}, Resolver: resolver, Retries: application.NoRetries()}
	})
}

// IgnoredKeys is the naive variant of D3: the key is recorded as client_ref
// and nothing else, so a retry after a lost answer runs again (FC02).
type IgnoredKeys struct{}

// Run executes every request as a new operation.
func (IgnoredKeys) Run(ctx context.Context, stores application.Stores, write application.Write) (idempotency.StoredResponse, error) {
	operationID, err := ledger.NewOperationID()
	if err != nil {
		return idempotency.StoredResponse{}, err
	}

	return write.Apply(ctx, stores, operationID)
}

type clientRefLookup interface {
	IsClientRefApplied(ctx context.Context, key idempotency.Key) (bool, error)
}

// ClientRefResolver is the naive resolve: it looks for the key in the
// ledger and answers, with no tombstone, so a request still in flight can
// apply after the answer "voided".
type ClientRefResolver struct {
	store clientRefLookup
}

// Resolve answers from the ledger as it is right now.
func (r ClientRefResolver) Resolve(ctx context.Context, request application.ResolveRequest) (application.Resolution, error) {
	isApplied, err := r.store.IsClientRefApplied(ctx, request.Key)
	if err != nil {
		return application.Resolution{}, err
	}

	if isApplied {
		return application.Resolution{Outcome: idempotency.OutcomeApplied}, nil
	}

	return application.Resolution{Outcome: idempotency.OutcomeVoided}, nil
}

// NoLocker is the naive variant of D2: it reads balances without locking
// them, so a read and the write that follows can interleave with another
// writer's (FC41).
type NoLocker struct{}

// Lock does nothing.
func (NoLocker) Lock(context.Context, application.Stores, []ledger.AccountID) error {
	return nil
}
