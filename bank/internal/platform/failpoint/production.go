//go:build !chaoslab

package failpoint

import "context"

func hit(context.Context, Name) error {
	return nil
}
