package postgres_test

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"centavo/bank/internal/infrastructure/postgres"
	"centavo/bank/migrations"
)

const (
	// Same image and init script as compose.yaml, so these tests also cover
	// the role setup the lab runs with.
	postgresImage  = "postgres:18.6"
	rolesScript    = "../../../../deploy/compose/postgres-init/01-roles.sh"
	bankDatabase   = "bank_robust"
	testPassword   = "test-only-password"
	startupTimeout = 2 * time.Minute
)

// database is shared by every test of the package; tests isolate themselves
// by creating their own accounts.
var database testDatabase

type testDatabase struct {
	migratorURL string
	appURL      string
}

type schema struct {
	hasAtomicLedger   bool
	hasBalanceLocking bool
	hasIdempotency    bool
	hasInbox          bool
	hasFencing        bool
}

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	flag.Parse()

	isShortRun := testing.Short()
	if isShortRun {
		return m.Run()
	}

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	container, err := startPostgres(ctx)
	defer terminate(container)

	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	database, err = databaseURLs(ctx, container)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	err = migrate(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	return m.Run()
}

func startPostgres(ctx context.Context) (*tcpostgres.PostgresContainer, error) {
	passwords := map[string]string{
		"BANK_MIGRATOR_PASSWORD": testPassword, "BANK_APP_PASSWORD": testPassword, "BANK_AUDITOR_PASSWORD": testPassword, "REPLICATOR_PASSWORD": testPassword,
	}

	container, err := tcpostgres.Run(ctx, postgresImage, tcpostgres.WithPassword(testPassword), tcpostgres.WithInitScripts(rolesScript), testcontainers.WithEnv(passwords), tcpostgres.BasicWaitStrategies())
	if err != nil {
		return container, fmt.Errorf("starting postgres: %w", err)
	}

	return container, nil
}

func databaseURLs(ctx context.Context, container *tcpostgres.PostgresContainer) (testDatabase, error) {
	host, err := container.Host(ctx)
	if err != nil {
		return testDatabase{}, err
	}

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return testDatabase{}, err
	}

	address := fmt.Sprintf("%s:%s", host, port.Port())

	return testDatabase{migratorURL: roleURL("bank_migrator", address), appURL: roleURL("bank_app", address)}, nil
}

func roleURL(role, address string) string {
	databaseURL := url.URL{
		Scheme: "postgres", User: url.UserPassword(role, testPassword), Host: address, Path: bankDatabase, RawQuery: "sslmode=disable",
	}

	return databaseURL.String()
}

func migrate(ctx context.Context) error {
	baseMigrations, err := migrations.Base()
	if err != nil {
		return err
	}

	err = postgres.MigrateBase(ctx, database.migratorURL, baseMigrations, discardLogger())
	if err != nil {
		return err
	}

	return migrateDefenses(ctx, true)
}

func migrateDefenses(ctx context.Context, isRobust bool) error {
	return migrateSchema(ctx, schema{hasAtomicLedger: isRobust, hasBalanceLocking: isRobust, hasIdempotency: isRobust, hasInbox: isRobust, hasFencing: isRobust})
}

func migrateSchema(ctx context.Context, wanted schema) error {
	return postgres.MigrateDefenses(ctx, database.migratorURL, schemaMigrations(wanted), discardLogger())
}

func defenseMigrations(isEnabled bool) []postgres.DefenseMigrations {
	return schemaMigrations(schema{hasAtomicLedger: isEnabled, hasBalanceLocking: isEnabled, hasIdempotency: isEnabled, hasInbox: isEnabled, hasFencing: isEnabled})
}

func schemaMigrations(wanted schema) []postgres.DefenseMigrations {
	enabled := map[string]bool{"atomic_ledger": wanted.hasAtomicLedger, "balance_locking": wanted.hasBalanceLocking, "idempotency": wanted.hasIdempotency, "inbox": wanted.hasInbox, "fencing": wanted.hasFencing}

	var list []postgres.DefenseMigrations

	for _, name := range []string{"atomic_ledger", "balance_locking", "idempotency", "inbox", "fencing"} {
		files, _, err := migrations.Defense(name)
		if err != nil {
			panic(err)
		}

		list = append(list, postgres.DefenseMigrations{Name: name, Files: files, IsEnabled: enabled[name]})
	}

	return list
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func terminate(container *tcpostgres.PostgresContainer) {
	err := testcontainers.TerminateContainer(container)
	if err != nil {
		fmt.Fprintln(os.Stderr, "terminating postgres:", err)
	}
}

func requireDatabase(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
}

func newPool(t *testing.T, databaseURL string) *pgxpool.Pool {
	t.Helper()
	requireDatabase(t)

	poolConfig := postgres.PoolConfig{DatabaseURL: databaseURL, MaxConnections: 4}

	pool, err := postgres.NewPool(t.Context(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(pool.Close)

	return pool
}
