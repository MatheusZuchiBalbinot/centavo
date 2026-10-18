package postgres

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// defenseVersionTablePrefix gives each defense its own goose version table.
const defenseVersionTablePrefix = "goose_defense_"

// ErrSchemaProfileMismatch means the process and the database disagree on
// which defense constraints exist; running anyway would test the wrong thing.
var ErrSchemaProfileMismatch = errors.New("the database schema does not match the defense profile; run bank migrate with the same CENTAVO_DEFENSES")

const appliedDefenseSQL = `SELECT to_regclass($1) IS NOT NULL`

// DefenseMigrations are the constraints of one defense and whether the
// profile wants them.
type DefenseMigrations struct {
	Name      string
	Files     fs.FS
	IsEnabled bool
}

// MigrateDefenses applies the migrations of every enabled defense and rolls
// back those of every disabled one, so the database ends up with exactly the
// constraints of its profile.
func MigrateDefenses(ctx context.Context, databaseURL string, defenses []DefenseMigrations, logger *slog.Logger) error {
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parsing database url: %w", err)
	}

	db := stdlib.OpenDB(*connConfig)
	defer closeQuietly(db, logger)

	for _, defense := range defenses {
		locker, err := lock.NewPostgresSessionLocker()
		if err != nil {
			return fmt.Errorf("creating migration lock: %w", err)
		}

		provider, err := goose.NewProvider(
			goose.DialectPostgres,
			db,
			defense.Files,
			goose.WithTableName(defenseVersionTablePrefix+defense.Name),
			goose.WithSessionLocker(locker),
			goose.WithSlog(logger),
		)
		if err != nil {
			return fmt.Errorf("creating %s migration provider: %w", defense.Name, err)
		}

		migrate := revertDefense
		if defense.IsEnabled {
			migrate = applyDefense
		}

		err = migrate(ctx, provider)
		if err != nil {
			return fmt.Errorf("migrating defense %s: %w", defense.Name, err)
		}

		logger.InfoContext(ctx, "defense schema reconciled", "defense", defense.Name, "enabled", defense.IsEnabled)
	}

	return nil
}

// CheckDefenseSchema refuses a process whose profile differs from the
// database's: with balance_locking off in the database but on in the process,
// for instance, a lost update would still be caught by the missing constraint
// and the round would prove nothing.
func CheckDefenseSchema(ctx context.Context, pool *pgxpool.Pool, defenses []DefenseMigrations) error {
	for _, defense := range defenses {
		isApplied, err := isDefenseApplied(ctx, pool, defense.Name)
		if err != nil {
			return err
		}

		if isApplied != defense.IsEnabled {
			return fmt.Errorf("%w: %s is %s in the database", ErrSchemaProfileMismatch, defense.Name, appliedText(isApplied))
		}
	}

	return nil
}

// applyDefense runs every migration of a defense that is on.
func applyDefense(ctx context.Context, provider *goose.Provider) error {
	_, err := provider.Up(ctx)

	return err
}

// revertDefense undoes every migration of a defense that is off.
func revertDefense(ctx context.Context, provider *goose.Provider) error {
	_, err := provider.DownTo(ctx, 0)

	return err
}

func isDefenseApplied(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	table := defenseVersionTablePrefix + name

	var hasTable bool

	err := pool.QueryRow(ctx, appliedDefenseSQL, table).Scan(&hasTable)
	if err != nil {
		return false, fmt.Errorf("reading defense schema: %w", err)
	}

	if !hasTable {
		return false, nil
	}

	var isApplied bool

	query := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE version_id > 0)`, pgx.Identifier{table}.Sanitize())

	err = pool.QueryRow(ctx, query).Scan(&isApplied)
	if err != nil {
		return false, fmt.Errorf("reading defense schema: %w", err)
	}

	return isApplied, nil
}

func appliedText(isApplied bool) string {
	if isApplied {
		return "applied"
	}

	return "not applied"
}
