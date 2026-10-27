//go:build !chaoslab

package clock

import "time"

func offset(time.Time) time.Duration {
	return 0
}
