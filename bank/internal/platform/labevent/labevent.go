// Package labevent lets the bank tell the chaos lab when one of its
// defenses acts. Only chaoslab builds record anything; in production every
// call is a no-op, so the lab's vocabulary never reaches the real binary.
package labevent

import "context"

type Defense string

const (
	// LeaseAcquired: a worker took a singleton's lease, a fresh one or one a
	// stopped worker left behind.
	LeaseAcquired Defense = "lease.acquired"
	// FencingRefused: a write under a lease that moved on was refused.
	FencingRefused   Defense = "fencing.refused"
	IdempotentReplay Defense = "idempotency.replayed"
	// OutboxResumed: a relay picked up PIX that already had an attempt.
	OutboxResumed Defense = "outbox.resumed"
	// OutboxFenced: a relay's result was discarded because another took the
	// message after its lease expired.
	OutboxFenced Defense = "outbox.fenced"
	// Reconciled: the reconciliation booked what the partner's statement
	// showed and the bank had missed.
	Reconciled Defense = "reconciliation.corrected"
)

// detail names the resource or the case, and count says how many items one
// action covered.
func Record(ctx context.Context, defense Defense, detail string, count int) {
	record(ctx, defense, detail, count)
}

// A lease it just took, not one it renewed.
func RecordLease(ctx context.Context, resource string, token int64) {
	recordLease(ctx, resource, token)
}
