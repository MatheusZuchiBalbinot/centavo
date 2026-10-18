package application

import (
	"context"
	"time"

	"centavo/bank/internal/platform/clock"
)

// Lease names a singleton loop's lease and the worker that wants it: two
// strings the compiler could not tell apart as separate arguments.
type Lease struct {
	Resource string
	Holder   string
}

// LeasePolicy is defense D11's strategy: how a singleton loop (sweeper,
// reconciler) knows it may still write.
type LeasePolicy interface {
	// Acquire takes or renews the lease; false means another holder has it.
	Acquire(ctx context.Context, stores Stores, lease Lease, ttl time.Duration) (int64, bool, error)
	// Holds runs inside the protected write's transaction.
	Holds(ctx context.Context, stores Stores, lease Lease, token int64) (bool, error)
	// MoveCursor writes the reconciliation cursor in the batch's commit.
	MoveCursor(ctx context.Context, stores Stores, from, to int64) (bool, error)
}

// FencedLeases is D11 (docs/domain.md, section 13.9): expiry by the
// database's now(), the token checked FOR SHARE in the very transaction
// that writes, and, as a second line, a compare-and-set on the cursor (with
// partner_seq UNIQUE in the schema). A paused or skewed worker cannot write.
type FencedLeases struct{}

// Acquire takes the lease by the database's clock.
func (FencedLeases) Acquire(ctx context.Context, stores Stores, lease Lease, ttl time.Duration) (int64, bool, error) {
	return stores.AcquireLease(ctx, lease, ttl)
}

// Holds checks holder and token in the write's transaction.
func (FencedLeases) Holds(ctx context.Context, stores Stores, lease Lease, token int64) (bool, error) {
	return stores.HoldsLease(ctx, lease, token)
}

// MoveCursor advances only from where the batch started.
func (FencedLeases) MoveCursor(ctx context.Context, stores Stores, from, to int64) (bool, error) {
	return stores.AdvanceCursor(ctx, from, to)
}

// LocalClockLeases is the naive variant: the worker computes expiry with its
// own wall clock, believes the check it made before the write, and saves
// the cursor blindly. A worker whose clock runs ahead takes a live lease
// (FC35), and one that paused past the TTL writes as if it still held it
// (FC36).
type LocalClockLeases struct{}

// Acquire takes the lease by this process's clock.
func (LocalClockLeases) Acquire(ctx context.Context, stores Stores, lease Lease, ttl time.Duration) (int64, bool, error) {
	return stores.AcquireLeaseAt(ctx, lease, clock.Now(), ttl)
}

// Holds trusts the check made when the lease was taken.
func (LocalClockLeases) Holds(context.Context, Stores, Lease, int64) (bool, error) {
	return true, nil
}

// MoveCursor overwrites the cursor.
func (LocalClockLeases) MoveCursor(ctx context.Context, stores Stores, _, to int64) (bool, error) {
	return true, stores.SaveCursor(ctx, to)
}
