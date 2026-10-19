//go:build !chaoslab

package labevent

import "context"

func record(context.Context, Defense, string, int) {}

func recordLease(context.Context, string, int64) {}
