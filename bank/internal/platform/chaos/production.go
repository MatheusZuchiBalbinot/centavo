//go:build !chaoslab

package chaos

import (
	"errors"
	"net/http"
)

var errNotAChaosBuild = errors.New("chaos-kill exists only in chaoslab builds")

// Setup does nothing in a production build.
func Setup(Config) error {
	return nil
}

// Server is nil in a production build: there is no chaos port at all.
func Server(Config) *http.Server {
	return nil
}

// KillMain refuses to run in a production build.
func KillMain(string) error {
	return errNotAChaosBuild
}
