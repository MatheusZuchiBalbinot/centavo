//go:build chaoslab

package naive

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/infrastructure/postgres"
)

// NewUnitOfWork is the naive variant of atomic_ledger: every statement commits
// on its own, so a crash between the debit and the credit destroys money.
func NewUnitOfWork(pool *pgxpool.Pool) application.UnitOfWork {
	return postgres.NewAutocommit(pool)
}

// NewIdempotency is the naive variant of idempotency: keys only recorded, a
// resolve without tombstones and no retries.
func NewIdempotency(pool *pgxpool.Pool) application.IdempotencyStrategy {
	resolver := ClientRefResolver{store: postgres.NewLedgerStore(pool)}

	return application.IdempotencyStrategy{Keys: IgnoredKeys{}, Resolver: resolver, Retries: application.NoRetries()}
}

// IgnoredKeys is the naive variant of idempotency: the key is recorded as
// client_ref and nothing else, so a retry after a lost answer runs again.
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

// NoLocker is the naive variant of balance_locking: it reads balances without
// locking them, so a read and the write that follows can interleave with
// another writer's.
type NoLocker struct{}

// Lock does nothing.
func (NoLocker) Lock(context.Context, application.PositionLocker, []ledger.AccountID) error {
	return nil
}
