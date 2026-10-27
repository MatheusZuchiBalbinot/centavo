//go:build !chaoslab

package chaoskill

import "errors"

var errNotAChaosBuild = errors.New("chaos-kill exists only in chaoslab builds")

func KillMain(string) error {
	return errNotAChaosBuild
}
