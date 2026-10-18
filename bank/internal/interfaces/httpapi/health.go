// Package httpapi translates HTTP requests into use cases and back.
package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// HealthPath is the admin route the lab and the container healthcheck poll.
const HealthPath = "/internal/health"

// databaseCheckTimeout keeps a frozen or partitioned database from hanging
// the healthcheck, which runs under chaos all the time.
const databaseCheckTimeout = 2 * time.Second

type healthStatus string

const (
	healthStatusOK          healthStatus = "ok"
	healthStatusUnavailable healthStatus = "unavailable"
)

type checkResult string

const (
	checkResultOK    checkResult = "ok"
	checkResultError checkResult = "error"
)

// Pinger checks that a dependency is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// BuildInfo identifies the running process in the health response.
type BuildInfo struct {
	Service    string
	Version    string
	ChaosBuild bool
	Env        string
	Defenses   string
	// DefenseNotes explain profiles where a defense depends on one that is off.
	DefenseNotes []string
}

type healthResponse struct {
	Service    string                 `json:"service"`
	Version    string                 `json:"version"`
	ChaosBuild bool                   `json:"chaos_build"`
	Env        string                 `json:"env"`
	Defenses   string                 `json:"defenses"`
	Notes      []string               `json:"defense_notes,omitempty"`
	Status     healthStatus           `json:"status"`
	Checks     map[string]checkResult `json:"checks"`
}

// NewAdminHandler serves the admin port (9090).
func NewAdminHandler(info BuildInfo, database Pinger) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET "+HealthPath, healthHandler{info: info, database: database})

	return mux
}

type healthHandler struct {
	info     BuildInfo
	database Pinger
}

func (h healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), databaseCheckTimeout)
	defer cancel()

	pingErr := h.database.Ping(ctx)
	httpStatus, response := h.report(pingErr)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)

	// The status line is already written; an encode error means the client is gone.
	_ = json.NewEncoder(w).Encode(response)
}

func (h healthHandler) report(pingErr error) (int, healthResponse) {
	if pingErr != nil {
		return http.StatusServiceUnavailable, h.response(healthStatusUnavailable, checkResultError)
	}

	return http.StatusOK, h.response(healthStatusOK, checkResultOK)
}

func (h healthHandler) response(status healthStatus, databaseResult checkResult) healthResponse {
	return healthResponse{
		Service: h.info.Service, Version: h.info.Version, ChaosBuild: h.info.ChaosBuild, Env: h.info.Env,
		Defenses: h.info.Defenses, Notes: h.info.DefenseNotes, Status: status, Checks: map[string]checkResult{"database": databaseResult},
	}
}
