//go:build !chaoslab

package clock

import "time"

// offset is always zero in a production build: there is no skew to set.
func offset(time.Time) time.Duration {
	return 0
}
