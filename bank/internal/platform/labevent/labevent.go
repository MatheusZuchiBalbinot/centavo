// Package labevent lets the bank tell the chaos lab when one of its defenses
// acts: a lease taken over, a stale write refused, a retry answered from the
// stored response. Only chaoslab builds record anything; in production every
// call is a no-op, so the lab's vocabulary never reaches the real binary.
package labevent

import "context"

// Defense names what the bank did to protect a guarantee.
type Defense string

// The defenses the lab shows as they act.
const (
	// LeaseAcquired: a worker took a singleton's lease, a fresh one or one a
	// stopped worker left behind.
	LeaseAcquired Defense = "lease.acquired"
	// FencingRefused: a write under a lease that moved on was refused.
	FencingRefused Defense = "fencing.refused"
	// IdempotentReplay: a repeated write got the stored answer, no new effect.
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

// Record tells the lab that a defense acted; detail names the resource or
// the case, and count says how many items one action covered.
func Record(ctx context.Context, defense Defense, detail string, count int) {
	record(ctx, defense, detail, count)
}

// RecordLease tells the lab that this process holds a singleton's lease
// under a new token: a lease it just took, not one it renewed.
func RecordLease(ctx context.Context, resource string, token int64) {
	recordLease(ctx, resource, token)
}
