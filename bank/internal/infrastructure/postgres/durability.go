package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotDurable = errors.New("synchronous_commit is not on: a confirmed commit could be lost in a crash")

// As the bank's own connections see it.
func CheckDurability(ctx context.Context, pool *pgxpool.Pool) error {
	var setting string

	err := pool.QueryRow(ctx, `SHOW synchronous_commit`).Scan(&setting)
	if err != nil {
		return fmt.Errorf("reading synchronous_commit: %w", err)
	}

	if setting != "on" {
		return fmt.Errorf("%w (it is %q)", ErrNotDurable, setting)
	}

	return nil
}
