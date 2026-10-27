// Package audit is the live auditor. It feeds the panel; the verdict always
// belongs to the checker.
package audit

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const Interval = 5 * time.Second

// queryTimeout keeps a frozen database from piling up audits.
const queryTimeout = 3 * time.Second

const balancesSQL = `
SELECT coalesce(sum(b.balance_cents), 0)::bigint,
       min(b.balance_cents) FILTER (WHERE a.kind = 'customer')
FROM account_balances b
JOIN accounts a ON a.id = b.account_id`

type Reading struct {
	At time.Time `json:"at"`
	// Double entry makes it zero.
	SumCents int64 `json:"sum_cents"`
	// MinCustomerCents is nil while the bank has no customers.
	MinCustomerCents *int64 `json:"min_customer_balance_cents"`
	IsUnavailable    bool   `json:"unavailable"`
}

type Auditor struct {
	pool   *pgxpool.Pool
	logger *slog.Logger

	mu      sync.Mutex
	latest  Reading
	hasRead bool
}

func New(pool *pgxpool.Pool, logger *slog.Logger) *Auditor {
	return &Auditor{pool: pool, logger: logger}
}

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
