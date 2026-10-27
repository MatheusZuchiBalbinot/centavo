//go:build chaoslab

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/application"
)

// The naive variant of atomic_ledger (chaoslab builds only): each statement
// commits on its own. It lives here because it builds the store on the bare
// pool; infrastructure/naive wires it.
type Autocommit struct {
	pool *pgxpool.Pool
}

func NewAutocommit(pool *pgxpool.Pool) Autocommit {
	return Autocommit{pool: pool}
}

func (a Autocommit) Run(ctx context.Context, fn func(ctx context.Context, stores application.Stores) error) error {
	return fn(ctx, NewLedgerStore(a.pool))
}
