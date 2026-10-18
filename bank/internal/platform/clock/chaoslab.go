//go:build chaoslab

package clock

import (
	"errors"
	"sync"
	"time"
)

// maxSkew bounds a skew: enough for the key cleanup's +30h.
const maxSkew = 48 * time.Hour

// ErrInvalidSkew is a skew too large or with no lifetime.
var ErrInvalidSkew = errors.New("clock skew must be within 48h and have a positive ttl")

var skew = struct {
	mu        sync.Mutex
	offset    time.Duration
	expiresAt time.Time
}{}

// SetSkew moves this process's wall clock by offset until ttl passes, so a lab
// that dies leaves the clock right again.
func SetSkew(offset, ttl time.Duration, now time.Time) error {
	isValid := offset >= -maxSkew && offset <= maxSkew && ttl > 0
	if !isValid {
		return ErrInvalidSkew
	}

	skew.mu.Lock()
	defer skew.mu.Unlock()

	skew.offset = offset
	skew.expiresAt = now.Add(ttl)

	return nil
}

// ClearSkew puts the clock right.
func ClearSkew() {
	skew.mu.Lock()
	defer skew.mu.Unlock()

	skew.offset = 0
}

func offset(now time.Time) time.Duration {
	skew.mu.Lock()
	defer skew.mu.Unlock()

	if now.After(skew.expiresAt) {
		return 0
	}

	return skew.offset
}
