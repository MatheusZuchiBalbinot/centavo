package httpapi

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// The named limits of the client API, per route group and for every caller
// together: the bank sits behind one proxy, whose X-Forwarded-For it does not
// trust, so a per-client limit would only see the proxy. They sit far above
// the lab's load and stop a runaway client before PostgreSQL does.
const (
	writesPerSecond   = 2_000
	writesBurst       = 4_000
	resolvePerSecond  = 500
	resolveBurst      = 1_000
	webhookPerSecond  = 1_000
	webhookBurst      = 2_000
	retryAfterSeconds = 1
)

// limiter is a token bucket: burst tokens, refilled at perSecond.
type limiter struct {
	mu        sync.Mutex
	perSecond float64
	burst     float64
	tokens    float64
	last      time.Time
	now       func() time.Time
}

// rateLimits are the limiters of the route groups, built once per handler.
type rateLimits struct {
	writes  *limiter
	resolve *limiter
	webhook *limiter
}

func newRateLimits(now func() time.Time) rateLimits {
	return rateLimits{
		writes:  newLimiter(writesPerSecond, writesBurst, now),
		resolve: newLimiter(resolvePerSecond, resolveBurst, now),
		webhook: newLimiter(webhookPerSecond, webhookBurst, now),
	}
}

func newLimiter(perSecond, burst int, now func() time.Time) *limiter {
	return &limiter{perSecond: float64(perSecond), burst: float64(burst), tokens: float64(burst), last: now(), now: now}
}

// allow takes one token, if there is one.
func (l *limiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.perSecond)
	l.last = now

	if l.tokens < 1 {
		return false
	}

	l.tokens--

	return true
}

// limited refuses a request with 429 when its limiter is empty; nothing of
// it ran, so the client may retry with the same key.
func (a apiHandlers) limited(l *limiter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if l.allow() {
			next(w, r)

			return
		}

		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds))
		writeError(w, r, a.logger, errRateLimited)
	}
}
