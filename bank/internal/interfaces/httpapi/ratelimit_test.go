package httpapi

import (
	"testing"
	"time"
)

// TestLimiter_RefusesPastTheBurstAndRefillsWithTime: a burst goes through,
// the next request waits for the refill, and time brings tokens back.
func TestLimiter_RefusesPastTheBurstAndRefillsWithTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	bucket := newLimiter(10, 3, clock)

	for k := range 3 {
		if !bucket.allow() {
			t.Fatalf("request %d of the burst refused", k)
		}
	}

	if bucket.allow() {
		t.Fatal("a request past the burst went through")
	}

	now = now.Add(100 * time.Millisecond)

	if !bucket.allow() {
		t.Fatal("a refilled token was refused")
	}
}
