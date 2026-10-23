package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
)

// acquireLeaseSQL is the acquisition: the token only grows when the lease
// changes hands or had expired.
const acquireLeaseSQL = `
UPDATE worker_leases
SET token = CASE WHEN holder = @holder AND expires_at > now() THEN token ELSE token + 1 END,
    holder = @holder,
    expires_at = now() + @ttl_ms::bigint * interval '1 millisecond'
WHERE resource = @resource
  AND (holder IS NULL OR expires_at <= now() OR holder = @holder)
RETURNING token`

// acquireLeaseAtSQL is the naive acquisition: the same rule, but every time
// in it is the caller's wall clock instead of the database's.
const acquireLeaseAtSQL = `
UPDATE worker_leases
SET token = CASE WHEN holder = @holder AND expires_at > @now THEN token ELSE token + 1 END,
    holder = @holder,
    expires_at = @now::timestamptz + @ttl_ms::bigint * interval '1 millisecond'
WHERE resource = @resource
  AND (holder IS NULL OR expires_at <= @now OR holder = @holder)
RETURNING token`

// holdsLeaseSQL takes FOR SHARE on a lease still ours: no one can acquire
// it while the transaction that checked it is open.
const holdsLeaseSQL = `
SELECT 1 FROM worker_leases
WHERE resource = @resource AND holder = @holder AND token = @token AND expires_at > now()
FOR SHARE`

// stuckSinceSQL picks the transfers in created or sent untouched for longer
// than @older_ms; it matches external_transfers_in_flight, the partial index
// on their updated_at.
const stuckSinceSQL = "status IN ('created', 'sent') AND updated_at < now() - @older_ms::bigint * interval '1 millisecond'"

// stuckTransferRow is a transfer the sweeper looks at again.
type stuckTransferRow struct {
	ID         uuid.UUID `db:"id"`
	EndToEndID string    `db:"end_to_end_id"`
	ClientRef  string    `db:"client_ref"`
}

// leaseStore keeps the leases of the singleton loops.
type leaseStore struct {
	db querier
}

// AcquireLease takes or renews the lease.
func (s leaseStore) AcquireLease(ctx context.Context, lease application.Lease, ttl time.Duration) (int64, bool, error) {
	var token int64

	values := args{"resource": lease.Resource, "holder": lease.Holder, "ttl_ms": ttl.Milliseconds()}
	isHeld, err := scanOptionalRow(ctx, s.db, newStatement("acquiring lease "+lease.Resource, acquireLeaseSQL, values), &token)

	return token, isHeld, err
}

// AcquireLeaseAt takes or renews the lease by the caller's clock.
func (s leaseStore) AcquireLeaseAt(ctx context.Context, lease application.Lease, now time.Time, ttl time.Duration) (int64, bool, error) {
	var token int64

	values := args{"resource": lease.Resource, "holder": lease.Holder, "now": now, "ttl_ms": ttl.Milliseconds()}
	isHeld, err := scanOptionalRow(ctx, s.db, newStatement("acquiring lease "+lease.Resource, acquireLeaseAtSQL, values), &token)

	return token, isHeld, err
}

// HoldsLease checks the lease within the caller's transaction.
func (s leaseStore) HoldsLease(ctx context.Context, lease application.Lease, token int64) (bool, error) {
	var one int

	values := args{"resource": lease.Resource, "holder": lease.Holder, "token": token}

	return scanOptionalRow(ctx, s.db, newStatement("checking lease "+lease.Resource, holdsLeaseSQL, values), &one)
}

// ListStuck returns the transfers that sat in created or sent for too long.
func (s leaseStore) ListStuck(ctx context.Context, olderThan time.Duration, limit int) ([]application.StuckTransfer, error) {
	rows, err := selectFrom[stuckTransferRow]("listing stuck transfers", externalTransfersTable).
		Where(stuckSinceSQL, args{"older_ms": olderThan.Milliseconds()}).
		OrderBy("updated_at").
		Limit(limit).
		All(ctx, s.db)
	if err != nil {
		return nil, err
	}

	stuck := make([]application.StuckTransfer, len(rows))

	for k, row := range rows {
		stuck[k] = application.StuckTransfer{ID: transfer.ID(row.ID), EndToEndID: transfer.EndToEndID(row.EndToEndID), ClientRef: row.ClientRef}
	}

	return stuck, nil
}
