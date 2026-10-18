// Package audit is the live auditor: every few seconds it reads the bank's
// materialized balances with bank_auditor and reports the two checks of version
// 0. It feeds the panel; the verdict always belongs to the checker.
package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Interval is how often the auditor reads the bank.
const Interval = 5 * time.Second

// queryTimeout keeps a frozen database from piling up audits.
const queryTimeout = 3 * time.Second

const balancesSQL = `
SELECT coalesce(sum(b.balance_cents), 0)::bigint,
       min(b.balance_cents) FILTER (WHERE a.kind = 'customer')
FROM account_balances b
JOIN accounts a ON a.id = b.account_id`

// Reading is one audit.
type Reading struct {
	At time.Time `json:"at"`
	// SumCents is the sum of every materialized balance; double entry makes it zero.
	SumCents int64 `json:"sum_cents"`
	// MinCustomerCents is nil while the bank has no customers.
	MinCustomerCents *int64 `json:"min_customer_balance_cents"`
	// IsUnavailable means the database did not answer; the checks are unknown.
	IsUnavailable bool `json:"unavailable"`
}

// Auditor keeps the latest reading.
type Auditor struct {
	pool   *pgxpool.Pool
	logger *slog.Logger

	mu      sync.Mutex
	latest  Reading
	hasRead bool
}

// New builds an auditor over a bank_auditor pool.
func New(pool *pgxpool.Pool, logger *slog.Logger) *Auditor {
	return &Auditor{pool: pool, logger: logger}
}

// Run audits until ctx ends.
func (a *Auditor) Run(ctx context.Context) {
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()

	for {
		a.store(a.read(ctx))

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Latest returns the last reading, and false before the first one.
func (a *Auditor) Latest() (Reading, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.latest, a.hasRead
}

func (a *Auditor) store(reading Reading) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.latest = reading
	a.hasRead = true
}

func (a *Auditor) read(ctx context.Context) Reading {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()

	reading := Reading{At: time.Now().UTC()}

	err := pgx.BeginTxFunc(ctx, a.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, balancesSQL).Scan(&reading.SumCents, &reading.MinCustomerCents)
	})
	if err != nil {
		a.logger.WarnContext(ctx, "live audit failed", "error", err)

		return Reading{At: reading.At, IsUnavailable: true}
	}

	return reading
}
