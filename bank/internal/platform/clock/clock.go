// Package clock is the bank's wall clock. Every wall-clock read that decides
// something goes through Now, so the chaoslab build can skew it per process.
// Durations measured with the monotonic clock (time.Since) and the database's
// now() never move.
package clock

import "time"

func Now() time.Time {
	now := time.Now()

	return now.Add(offset(now))
}
