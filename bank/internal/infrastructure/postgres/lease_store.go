package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
)

// acquireLeaseSQL is the acquisition: the token only grows when the lease
// changes hands or had expired.
const acquireLeaseSQL = `
UPDATE worker_leases
SET token = CASE WHEN holder = $2 AND expires_at > now() THEN token ELSE token + 1 END,
    holder = $2,
    expires_at = now() + $3::bigint * interval '1 millisecond'
WHERE resource = $1
  AND (holder IS NULL OR expires_at <= now() OR holder = $2)
RETURNING token`

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

// holdsLeaseSQL takes FOR SHARE on a lease still ours: no one can acquire
// it while the transaction that checked it is open.
const holdsLeaseSQL = `
SELECT 1 FROM worker_leases
WHERE resource = $1 AND holder = $2 AND token = $3 AND expires_at > now()
FOR SHARE`

// listStuckSQL uses external_transfers_in_flight, the partial index on
// updated_at of the transfers in created or sent.
const listStuckSQL = `
SELECT id, end_to_end_id, client_ref
FROM external_transfers
WHERE status IN ('created', 'sent') AND updated_at < now() - $1::bigint * interval '1 millisecond'
ORDER BY updated_at
LIMIT $2`

// leaseStore keeps the leases of the singleton loops.
type leaseStore struct {
	db querier
}

// AcquireLease takes or renews the lease.
func (s leaseStore) AcquireLease(ctx context.Context, lease application.Lease, ttl time.Duration) (int64, bool, error) {
	var token int64

	acquire := newStatement("acquiring lease "+lease.Resource, acquireLeaseSQL, lease.Resource, lease.Holder, ttl.Milliseconds())
	isHeld, err := scanOptionalRow(ctx, s.db, acquire, &token)

	return token, isHeld, err
}

// AcquireLeaseAt takes or renews the lease by the caller's clock.
func (s leaseStore) AcquireLeaseAt(ctx context.Context, lease application.Lease, now time.Time, ttl time.Duration) (int64, bool, error) {
	var token int64

	acquire := newStatement("acquiring lease "+lease.Resource, acquireLeaseAtSQL, lease.Resource, lease.Holder, now, ttl.Milliseconds())
	isHeld, err := scanOptionalRow(ctx, s.db, acquire, &token)

	return token, isHeld, err
}

// HoldsLease checks the lease within the caller's transaction.
func (s leaseStore) HoldsLease(ctx context.Context, lease application.Lease, token int64) (bool, error) {
	var one int

	check := newStatement("checking lease "+lease.Resource, holdsLeaseSQL, lease.Resource, lease.Holder, token)

	return scanOptionalRow(ctx, s.db, check, &one)
}

// ListStuck returns the transfers that sat in created or sent for too long.
func (s leaseStore) ListStuck(ctx context.Context, olderThan time.Duration, limit int) ([]application.StuckTransfer, error) {
	list := newStatement("listing stuck transfers", listStuckSQL, olderThan.Milliseconds(), limit)

	return queryRows(ctx, s.db, list, scanStuckTransfer)
}

func scanStuckTransfer(row pgx.CollectableRow) (application.StuckTransfer, error) {
	var (
		stuck application.StuckTransfer
		id    uuid.UUID
	)

	err := row.Scan(&id, &stuck.EndToEndID, &stuck.ClientRef)
	stuck.ID = transfer.ID(id)

	return stuck, err
}
