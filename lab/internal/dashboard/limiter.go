package dashboard

import (
	"sync"
	"time"
)

type limit struct {
	count  int
	window time.Duration
}

// For everyone together: each round takes the bank down on purpose for
// minutes.
var roundStartLimit = limit{count: 6, window: time.Hour}

// Each manual fault heals itself in seconds; the limit keeps a script from
// holding the bank down for the whole round.
var manualAttackLimit = limit{count: 12, window: time.Minute}

type windowLimiter struct {
	limit limit
	now   func() time.Time

	mu     sync.Mutex
	events []time.Time
}

func newWindowLimiter(rule limit) *windowLimiter {
	return &windowLimiter{limit: rule, now: time.Now}
}

func (l *windowLimiter) wait() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.forgetOld()

	if len(l.events) < l.limit.count {
		return 0
	}

	return l.events[0].Add(l.limit.window).Sub(l.now())
}

func (l *windowLimiter) record() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.forgetOld()
	l.events = append(l.events, l.now())
}

func (l *windowLimiter) forgetOld() {
	cutoff := l.now().Add(-l.limit.window)
	kept := l.events[:0]

	for _, at := range l.events {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}

	l.events = kept
}
