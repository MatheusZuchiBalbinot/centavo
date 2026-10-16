// Package dashboard serves the lab panel (docs/architecture.md, section 13).
package dashboard

import (
	"encoding/json"
	"net/http"
)

// HealthPath is the route the container healthcheck polls.
const HealthPath = "/internal/health"

type healthResponse struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// NewHandler serves the panel port (8090).
func NewHandler(version string) http.Handler {
	response := healthResponse{Service: "lab", Version: version, Status: "ok"}

	mux := http.NewServeMux()
	mux.Handle("GET "+HealthPath, healthHandler{response: response})

	return mux
}

type healthHandler struct {
	response healthResponse
}

func (h healthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// An encode error here means the client is gone; there is nothing left to report.
	_ = json.NewEncoder(w).Encode(h.response)
}
