package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
)

// acquireLeaseSQL is the acquisition of docs/domain.md, section 13.9: the
// token only grows when the lease changes hands or had expired.
const acquireLeaseSQL = `
UPDATE worker_leases
SET token = CASE WHEN holder = $2 AND expires_at > now() THEN token ELSE token + 1 END,
    holder = $2,
    expires_at = now() + $3::bigint * interval '1 millisecond'
WHERE resource = $1
  AND (holder IS NULL OR expires_at <= now() OR holder = $2)
RETURNING token`

// AcquireLease takes or renews the lease.
func (s LedgerStore) AcquireLease(ctx context.Context, resource, holder string, ttl time.Duration) (int64, bool, error) {
	var token int64

	err := s.db.QueryRow(ctx, acquireLeaseSQL, resource, holder, ttl.Milliseconds()).Scan(&token)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}

	if err != nil {
		return 0, false, fmt.Errorf("acquiring lease %s: %w", resource, err)
	}

	return token, true, nil
}

// acquireLeaseAtSQL is the naive acquisition: the same rule, but every time
// in it is the caller's wall clock instead of the database's.
const acquireLeaseAtSQL = `
UPDATE worker_leases
SET token = CASE WHEN holder = $2 AND expires_at > $3 THEN token ELSE token + 1 END,
    holder = $2,
    expires_at = $3::timestamptz + $4::bigint * interval '1 millisecond'
WHERE resource = $1
  AND (holder IS NULL OR expires_at <= $3 OR holder = $2)
RETURNING token`

// AcquireLeaseAt takes or renews the lease by the caller's clock.
func (s LedgerStore) AcquireLeaseAt(ctx context.Context, resource, holder string, now time.Time, ttl time.Duration) (int64, bool, error) {
	var token int64

	err := s.db.QueryRow(ctx, acquireLeaseAtSQL, resource, holder, now, ttl.Milliseconds()).Scan(&token)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}

	if err != nil {
		return 0, false, fmt.Errorf("acquiring lease %s: %w", resource, err)
	}

	return token, true, nil
}

// holdsLeaseSQL takes FOR SHARE on a lease still ours: no one can acquire
// it while the transaction that checked it is open.
const holdsLeaseSQL = `
SELECT 1 FROM worker_leases
WHERE resource = $1 AND holder = $2 AND token = $3 AND expires_at > now()
FOR SHARE`

// HoldsLease checks the lease within the caller's transaction.
func (s LedgerStore) HoldsLease(ctx context.Context, resource, holder string, token int64) (bool, error) {
	var one int

	err := s.db.QueryRow(ctx, holdsLeaseSQL, resource, holder, token).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("checking lease %s: %w", resource, err)
	}

	return true, nil
}

// listStuckSQL uses external_transfers_in_flight, the partial index on
// updated_at of the transfers in created or sent.
const listStuckSQL = `
SELECT id, end_to_end_id, client_ref
FROM external_transfers
WHERE status IN ('created', 'sent') AND updated_at < now() - $1::bigint * interval '1 millisecond'
ORDER BY updated_at
LIMIT $2`

// ListStuck returns the transfers that sat in created or sent for too long.
func (s LedgerStore) ListStuck(ctx context.Context, olderThan time.Duration, limit int) ([]application.StuckTransfer, error) {
	rows, err := s.db.Query(ctx, listStuckSQL, olderThan.Milliseconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("listing stuck transfers: %w", err)
	}

	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (application.StuckTransfer, error) {
		var (
			stuck application.StuckTransfer
			id    uuid.UUID
		)

		err := row.Scan(&id, &stuck.EndToEndID, &stuck.ClientRef)
		stuck.ID = transfer.ID(id)

		return stuck, err
	})
}
