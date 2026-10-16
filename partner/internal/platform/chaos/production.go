//go:build !chaoslab

package chaos

import (
	"net/http"

	"centavo/partner/internal/modes"
)

// Handler is nil in a production build: the partner cannot be told to lie.
func Handler(string, *modes.Registry) http.Handler {
	return nil
}
