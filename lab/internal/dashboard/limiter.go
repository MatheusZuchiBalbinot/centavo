package dashboard

import (
	"sync"
	"time"
)

// limit is how many events a window allows.
type limit struct {
	count  int
	window time.Duration
}

// roundStartLimit caps the rounds the panel starts, for everyone together:
// each one takes the bank down on purpose for minutes.
var roundStartLimit = limit{count: 6, window: time.Hour}

// windowLimiter counts events over a sliding window.
type windowLimiter struct {
	limit limit
	now   func() time.Time

	mu     sync.Mutex
	events []time.Time
}

func newWindowLimiter(rule limit) *windowLimiter {
	return &windowLimiter{limit: rule, now: time.Now}
}

// wait returns how long until the next event is allowed; zero means now.
func (l *windowLimiter) wait() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.forgetOld()

	if len(l.events) < l.limit.count {
		return 0
	}

	return l.events[0].Add(l.limit.window).Sub(l.now())
}

// record counts an event that happened.
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
