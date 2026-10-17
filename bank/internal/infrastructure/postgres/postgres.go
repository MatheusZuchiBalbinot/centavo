// Package postgres is the only place that knows SQL and the PostgreSQL driver.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// baseVersionTable keeps the base schema apart from the per-defense migration
// tables (docs/architecture.md, section 8.2, step 5).
const baseVersionTable = "goose_base_version"

// PoolConfig sizes a connection pool for one process role.
type PoolConfig struct {
	DatabaseURL    string
	MaxConnections int32
	// IsRelaxed turns synchronous_commit off on every connection: the naive
	// variant of defense D12, where a commit is acknowledged before its WAL
	// leaves shared memory (FC30).
	IsRelaxed bool
}

// NewPool creates a lazily connecting pool; reachability is reported by the
// health endpoint instead of blocking startup, since the database may be down
// under chaos.
func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parsing database url: %w", err)
	}

	poolConfig.MaxConns = cfg.MaxConnections

	if cfg.IsRelaxed {
		poolConfig.ConnConfig.RuntimeParams["synchronous_commit"] = "off"
	}

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating pool: %w", err)
	}

	return pool, nil
}

// MigrateBase applies the base migrations under a PostgreSQL advisory lock,
// so concurrent `bank migrate` runs serialize instead of racing.
func MigrateBase(ctx context.Context, databaseURL string, migrations fs.FS, logger *slog.Logger) error {
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parsing database url: %w", err)
	}

	db := stdlib.OpenDB(*connConfig)
	defer closeQuietly(db, logger)

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("creating migration lock: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations, goose.WithTableName(baseVersionTable), goose.WithSessionLocker(locker), goose.WithSlog(logger))
	if err != nil {
		return fmt.Errorf("creating migration provider: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("applying base migrations: %w", err)
	}

	logger.InfoContext(ctx, "base migrations applied", "applied", len(results))

	return nil
}

func closeQuietly(db *sql.DB, logger *slog.Logger) {
	err := db.Close()
	if err != nil {
		logger.Warn("closing migration connection", "error", err)
	}
}
