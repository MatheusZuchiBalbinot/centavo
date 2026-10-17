package postgres_test

import (
	"errors"
	"testing"

	"centavo/bank/internal/infrastructure/postgres"
)

// TestDurability_SynchronousCommitIsOnAtStartup is FC30 and PM-015: the
// robust bank sees synchronous_commit on, and the naive variant's
// connections are the ones the check refuses.
func TestDurability_SynchronousCommitIsOnAtStartup(t *testing.T) {
	requireDatabase(t)

	durable := newPool(t, database.appURL)

	err := postgres.CheckDurability(t.Context(), durable)
	if err != nil {
		t.Fatalf("a default connection is not durable: %v", err)
	}

	relaxed, err := postgres.NewPool(t.Context(), postgres.PoolConfig{DatabaseURL: database.appURL, MaxConnections: 2, IsRelaxed: true})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(relaxed.Close)

	err = postgres.CheckDurability(t.Context(), relaxed)
	if !errors.Is(err, postgres.ErrNotDurable) {
		t.Fatalf("relaxed connections passed the check: %v", err)
	}
}
