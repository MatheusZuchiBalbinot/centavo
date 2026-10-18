//go:build chaoslab

package chaos

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"time"

	"centavo/partner/internal/modes"
	"centavo/partner/internal/platform/chaosbuild"
)

// modesRoute is where the lab sets and clears the partner's lies.
const modesRoute = "/chaos/modes"

const (
	tokenHeader  = "X-Chaos-Token" //nolint:gosec // G101: a header name, not a secret.
	maxModeBytes = 8 << 10
)

// Handler serves PUT and DELETE /chaos/modes behind the chaos token.
func Handler(token string, registry *modes.Registry) http.Handler {
	controls := controls{registry: registry}

	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+modesRoute, controls.set)
	mux.HandleFunc("DELETE "+modesRoute, controls.clear)

	return requireToken(token, mux)
}

type controls struct {
	registry *modes.Registry
}

func (c controls) set(w http.ResponseWriter, r *http.Request) {
	var config modes.Config

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxModeBytes))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(&config)
	if err == nil {
		err = c.registry.Set(config, time.Now().UTC())
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)

		return
	}

	writeJSON(w, map[string]string{"participant_id": config.ParticipantID, "mode": "set"})
}

func (c controls) clear(w http.ResponseWriter, _ *http.Request) {
	c.registry.Clear()
	writeJSON(w, map[string]bool{"honest": true})
}

func requireToken(token string, next http.Handler) http.Handler {
	expected := []byte(token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received := []byte(r.Header.Get(tokenHeader))
		isAuthorized := len(expected) > 0 && subtle.ConstantTimeCompare(received, expected) == 1
		if !isAuthorized {
			http.Error(w, "missing or wrong "+tokenHeader, http.StatusUnauthorized)

			return
		}

		w.Header().Set("X-Chaos-Build", chaosbuild.Sentinel())
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")

	// An encode error means the lab went away; there is nobody to tell.
	_ = json.NewEncoder(w).Encode(body)
}
