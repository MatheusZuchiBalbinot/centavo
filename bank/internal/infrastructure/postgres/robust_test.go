package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/infrastructure/postgres"
)

const (
	uniqueViolation = "23505"
	checkViolation  = "23514"
)

// TestBalance_ConcurrentDebits_NeverOverdraw is a regression test: with
// balance_locking the debits queue on the balance row, and the eleventh one is
// refused.
func TestBalance_ConcurrentDebits_NeverOverdraw(t *testing.T) {
	b := newBank(t)
	source, destination := b.newCustomer(t), b.newCustomer(t)
	b.fund(t, source, 1_000)

	const debits = 20

	var (
		wg           sync.WaitGroup
		mu           sync.Mutex
		succeeded    int
		insufficient int
	)

	for range debits {
		wg.Go(func() {
			input := application.TransferInput{From: source, To: destination, Amount: amountOf(t, 100), Write: newWrite(t)}

			_, err := b.transfer.Execute(context.Background(), input)

			mu.Lock()
			defer mu.Unlock()

			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, ledger.ErrInsufficientFunds):
				insufficient++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		})
	}

	wg.Wait()

	isUnexpected := succeeded != 10 || insufficient != 10
	if isUnexpected {
		t.Fatalf("succeeded %d, insufficient %d; want 10 and 10", succeeded, insufficient)
	}

	assertChainedAndNeverNegative(t, b, source, 0)
	assertChainedAndNeverNegative(t, b, destination, 1_000)
}

func TestTransfer_OppositeDirections_NeverDeadlock(t *testing.T) {
	b := newBank(t)
	first, second := b.newCustomer(t), b.newCustomer(t)
	b.fund(t, first, 10_000)
	b.fund(t, second, 10_000)

	var wg sync.WaitGroup

	for i := range 50 {
		from, to := first, second
		if i%2 == 1 {
			from, to = second, first
		}

		wg.Go(func() {
			input := application.TransferInput{From: from, To: to, Amount: amountOf(t, 10), Write: newWrite(t)}

			_, err := b.transfer.Execute(context.Background(), input)
			if err != nil {
				t.Errorf("transfer %d: %v", i, err)
			}
		})
	}

	wg.Wait()

	assertChainedAndNeverNegative(t, b, first, 10_000)
	assertChainedAndNeverNegative(t, b, second, 10_000)
}

// assertChainedAndNeverNegative checks each account's entries chain and
// customer balances never go negative on one account, the way the checker does:
// positions 1..n, chained balances, materialized equal to the sum.
func assertChainedAndNeverNegative(t *testing.T, b bank, account ledger.AccountID, want int64) {
	t.Helper()

	lines := b.statement(t, account)

	var running int64

	for i, line := range lines {
		running += int64(line.Amount)

		isChained := line.Seq == ledger.Seq(i+1) && int64(line.BalanceAfter.Cents()) == running && running >= 0
		if !isChained {
			t.Fatalf("account %s, line %d: %+v (running sum %d)", account, i, line, running)
		}
	}

	view, err := b.getAccount.Execute(t.Context(), account)
	if err != nil {
		t.Fatal(err)
	}

	isUnexpected := int64(view.Balance.Cents()) != running || running != want
	if isUnexpected {
		t.Fatalf("account %s: materialized %d, entries sum %d, want %d", account, view.Balance.Cents(), running, want)
	}
}

// TestDefenseConstraints_CatchWhatTheLocksMiss checks the safety net of
// atomic_ledger and balance_locking: writes a skipped lock or a crash would let
// through fail at the database instead of corrupting the ledger.
func TestDefenseConstraints_CatchWhatTheLocksMiss(t *testing.T) {
	b := newBank(t)
	account := b.newCustomer(t)
	funding := b.fund(t, account, 100)

	cases := []struct {
		name string
		code string
		run  func(ctx context.Context, tx pgx.Tx) error
	}{
		{name: "two entries in the same position", code: uniqueViolation, run: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, account_id, amount_cents, account_seq, balance_after_cents) VALUES ($1, $2, 1, 1, 101)`, uuid.UUID(funding.TransactionID), uuid.UUID(account))

			return err
		}},
		{name: "customer balance below zero", code: checkViolation, run: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `UPDATE account_balances SET balance_cents = -1 WHERE account_id = $1`, uuid.UUID(account))

			return err
		}},
		{name: "unbalanced transaction at commit", code: checkViolation, run: func(ctx context.Context, tx pgx.Tx) error {
			id := uuid.New()

			_, err := tx.Exec(ctx, `INSERT INTO ledger_transactions (id, kind, operation_id, correlation_id) VALUES ($1, 'deposit', $2, 'test')`, id, uuid.New())
			if err != nil {
				return err
			}

			_, err = tx.Exec(ctx, `INSERT INTO ledger_entries (transaction_id, account_id, amount_cents, account_seq, balance_after_cents) VALUES ($1, $2, 5, 900, 105)`, id, uuid.UUID(account))

			return err
		}},
		{name: "transaction without entries at commit", code: checkViolation, run: func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO ledger_transactions (id, kind, operation_id, correlation_id) VALUES ($1, 'deposit', $2, 'test')`, uuid.New(), uuid.New())

			return err
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pgx.BeginTxFunc(t.Context(), b.pool, pgx.TxOptions{}, func(tx pgx.Tx) error { return tc.run(t.Context(), tx) })

			var pgErr *pgconn.PgError

			isRefused := errors.As(err, &pgErr) && pgErr.Code == tc.code
			if !isRefused {
				t.Fatalf("got %v, want SQLSTATE %s", err, tc.code)
			}
		})
	}
}

func TestCheckDefenseSchema_RefusesAMismatchedProfile(t *testing.T) {
	b := newBank(t)

	err := postgres.CheckDefenseSchema(t.Context(), b.pool, defenseMigrations(true))
	if err != nil {
		t.Fatalf("robust process on a robust database: %v", err)
	}

	err = postgres.CheckDefenseSchema(t.Context(), b.pool, defenseMigrations(false))
	if !errors.Is(err, postgres.ErrSchemaProfileMismatch) {
		t.Fatalf("naive process on a robust database: got %v", err)
	}
}

func TestMigrateDefenses_TakesTheConstraintsOutAndBackIn(t *testing.T) {
	b := newBank(t)

	t.Cleanup(func() {
		err := migrateDefenses(context.Background(), true)
		if err != nil {
			t.Fatalf("restoring the robust schema: %v", err)
		}
	})

	err := migrateDefenses(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}

	if hasConstraint(t, b, "balance_respects_floor") || hasTrigger(t, b, "ledger_entries_balanced") {
		t.Fatal("the naive schema still has defense constraints")
	}

	err = migrateDefenses(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}

	if !hasConstraint(t, b, "entry_unique_seq_per_account") || !hasTrigger(t, b, "ledger_transactions_have_entries") {
		t.Fatal("the robust schema is missing defense constraints")
	}
}

func hasConstraint(t *testing.T, b bank, name string) bool {
	t.Helper()

	return exists(t, b, `SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = $1)`, name)
}

func hasTrigger(t *testing.T, b bank, name string) bool {
	t.Helper()

	return exists(t, b, `SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname = $1)`, name)
}

func exists(t *testing.T, b bank, query, name string) bool {
	t.Helper()

	var found bool

	err := b.pool.QueryRow(t.Context(), query, name).Scan(&found)
	if err != nil {
		t.Fatal(err)
	}

	return found
}
