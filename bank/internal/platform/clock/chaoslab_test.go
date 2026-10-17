//go:build chaoslab

package clock

import (
	"testing"
	"time"
)

func TestSkew_MovesTheClockUntilItsTTL(t *testing.T) {
	t.Cleanup(ClearSkew)

	err := SetSkew(2*time.Minute, time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	drift := time.Until(Now())
	if drift < time.Minute {
		t.Fatalf("skewed clock is %s ahead", drift)
	}

	err = SetSkew(2*time.Minute, time.Nanosecond, time.Now().Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}

	if time.Until(Now()) > time.Second {
		t.Fatal("an expired skew still moves the clock")
	}

	err = SetSkew(100*time.Hour, time.Hour, time.Now())
	if err == nil {
		t.Fatal("a 100h skew was accepted")
	}
}
