//go:build chaoslab

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/application"
)

// Autocommit runs a use case without a transaction: each statement commits on
// its own. It is the naive variant of atomic_ledger (chaoslab builds only); it
// lives here because it builds the store on the bare pool, and
// infrastructure/naive wires it.
type Autocommit struct {
	pool *pgxpool.Pool
}

// NewAutocommit wraps a pool.
func NewAutocommit(pool *pgxpool.Pool) Autocommit {
	return Autocommit{pool: pool}
}

// Run hands fn stores that commit every statement immediately.
func (a Autocommit) Run(ctx context.Context, fn func(ctx context.Context, stores application.Stores) error) error {
	return fn(ctx, NewLedgerStore(a.pool))
}
