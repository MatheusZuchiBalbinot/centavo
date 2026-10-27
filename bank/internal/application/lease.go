package application

import (
	"context"
	"time"

	"centavo/bank/internal/platform/clock"
	"centavo/bank/internal/platform/labevent"
)

// Lease names a singleton loop's lease and the worker that wants it: two
// strings the compiler could not tell apart as separate arguments.
type Lease struct {
	Resource string
	Holder   string
}

// LeasePolicy is the fencing defense's strategy: how a singleton loop (sweeper,
// reconciler) knows it may still write.
type LeasePolicy interface {
	Acquire(ctx context.Context, stores LeaseStore, lease Lease, ttl time.Duration) (int64, bool, error)
	// Holds runs inside the protected write's transaction.
	Holds(ctx context.Context, stores LeaseStore, lease Lease, token int64) (bool, error)
	MoveCursor(ctx context.Context, stores ReconciliationStore, from, to int64) (bool, error)
}

// FencedLeases is the fencing defense: expiry by the database's now(), the
// token checked FOR SHARE in the very transaction that writes, and, as a second
// line, a compare-and-set on the cursor (with partner_seq UNIQUE in the
// schema). A paused or skewed worker cannot write.
type FencedLeases struct{}

// LocalClockLeases is the naive variant: the worker computes expiry with its
// own wall clock, believes the check it made before the write, and saves the
// cursor blindly. A worker whose clock runs ahead takes a live lease, and one
// that paused past the TTL writes as if it still held it.
type LocalClockLeases struct{}

func (FencedLeases) Acquire(ctx context.Context, stores LeaseStore, lease Lease, ttl time.Duration) (int64, bool, error) {
	token, isHeld, err := stores.AcquireLease(ctx, lease, ttl)
	if isHeld {
		labevent.RecordLease(ctx, lease.Resource, token)
	}

	return token, isHeld, err
}

func (FencedLeases) Holds(ctx context.Context, stores LeaseStore, lease Lease, token int64) (bool, error) {
	isHeld, err := stores.HoldsLease(ctx, lease, token)

	isRefused := err == nil && !isHeld
	if isRefused {
		labevent.Record(ctx, labevent.FencingRefused, lease.Resource, 1)
	}

	return isHeld, err
}

func (FencedLeases) MoveCursor(ctx context.Context, stores ReconciliationStore, from, to int64) (bool, error) {
	return stores.AdvanceCursor(ctx, from, to)
}

func (LocalClockLeases) Acquire(ctx context.Context, stores LeaseStore, lease Lease, ttl time.Duration) (int64, bool, error) {
	return stores.AcquireLeaseAt(ctx, lease, clock.Now(), ttl)
}

func (LocalClockLeases) Holds(context.Context, LeaseStore, Lease, int64) (bool, error) {
	return true, nil
}

func (LocalClockLeases) MoveCursor(ctx context.Context, stores ReconciliationStore, _, to int64) (bool, error) {
	return true, stores.SaveCursor(ctx, to)
}
