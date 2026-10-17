// Package clock is the bank's wall clock (docs/chaos-bot.md, section 6).
// Every wall-clock read that decides something goes through Now, so the
// chaoslab build can skew it per process. Durations measured with the
// monotonic clock (time.Since) and the database's now() never move.
package clock

import "time"

// Now is the process's wall clock: time.Now plus the skew the lab set.
func Now() time.Time {
	return time.Now().Add(offset(time.Now()))
}
