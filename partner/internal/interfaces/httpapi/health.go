// Package httpapi exposes the partner over HTTP.
package httpapi

import (
	"encoding/json"
	"net/http"
)

// HealthPath is the admin route the lab and the container healthcheck poll.
const HealthPath = "/internal/health"

const healthStatusOK = "ok"

// BuildInfo identifies the running process in the health response.
type BuildInfo struct {
	Version      string
	IsChaosBuild bool
	Env          string
}

type healthResponse struct {
	Service      string `json:"service"`
	Version      string `json:"version"`
	IsChaosBuild bool   `json:"chaos_build"`
	Env          string `json:"env"`
	Status       string `json:"status"`
}

// NewAdminHandler serves the admin and chaos port (9092). The lab-only
// routes live here, never on the client port; chaos is nil outside chaoslab builds.
func NewAdminHandler(info BuildInfo, api API, chaos http.Handler) http.Handler {
	response := healthResponse{Service: "partner", Version: info.Version, IsChaosBuild: info.IsChaosBuild, Env: info.Env, Status: healthStatusOK}

	mux := http.NewServeMux()
	mux.Handle("GET "+HealthPath, healthHandler{response: response})
	mux.HandleFunc("POST "+incomingRoute, api.incoming)
	mux.HandleFunc("GET "+truthRoute, api.truthExport)
	mux.HandleFunc("GET "+pendingRoute, api.pending)

	if chaos != nil {
		mux.Handle(chaosPrefix, chaos)
	}

	return withSecurityHeaders(mux)
}

type healthHandler struct {
	response healthResponse
}

func (h healthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// An encode error here means the client is gone; there is nothing left to report.
	_ = json.NewEncoder(w).Encode(h.response)
}
