//go:build !chaoslab

package chaoskill

import "errors"

var errNotAChaosBuild = errors.New("chaos-kill exists only in chaoslab builds")

// KillMain refuses to run in a production build.
func KillMain(string) error {
	return errNotAChaosBuild
}
