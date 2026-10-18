//go:build chaoslab

package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/application"
	"centavo/bank/internal/infrastructure/naive"
)

// newNaiveVariants builds the naive variants this chaoslab binary carries.
func newNaiveVariants(pool *pgxpool.Pool) (naiveVariants, error) {
	return naiveVariants{unitOfWork: naive.NewUnitOfWork(pool), locker: application.Locker(naive.NoLocker{}), idempotency: naive.NewIdempotency(pool)}, nil
}
