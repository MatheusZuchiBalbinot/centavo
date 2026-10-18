//go:build !chaoslab

package main

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/platform/defenses"
)

// newNaiveVariants refuses: the production binary has no naive code, and a
// profile that turns a defense off cannot run on it.
func newNaiveVariants(*pgxpool.Pool) (naiveVariants, error) {
	return naiveVariants{}, defenses.ErrNoNaiveVariant
}
