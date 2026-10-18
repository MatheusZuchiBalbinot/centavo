package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/application"
)

// UnitOfWork runs a use case in one READ COMMITTED transaction (the
// atomic_ledger defense): every statement commits together or none does, and
// the deferred zero-sum triggers run at the commit.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

// NewUnitOfWork wraps a pool.
func NewUnitOfWork(pool *pgxpool.Pool) UnitOfWork {
	return UnitOfWork{pool: pool}
}

// Run commits when fn returns nil and rolls back otherwise. A process that
// dies before the commit leaves nothing behind: PostgreSQL rolls back the
// transaction of a dropped connection. Storage failures come back classified
// as transient, ambiguous or unavailable.
func (u UnitOfWork) Run(ctx context.Context, fn func(ctx context.Context, stores application.Stores) error) error {
	options := pgx.TxOptions{IsoLevel: pgx.ReadCommitted}

	err := pgx.BeginTxFunc(ctx, u.pool, options, func(tx pgx.Tx) error {
		return fn(ctx, LedgerStore{db: tx})
	})

	return classify(err)
}
