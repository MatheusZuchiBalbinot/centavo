// Package dashboard serves the lab panel (docs/architecture.md, section 13).
// This is the minimal invariants page of phase F2: the live auditor's checks
// and the verdict of the last round.
package dashboard

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"centavo/lab/internal/audit"
)

// HealthPath is the route the container healthcheck polls.
const HealthPath = "/internal/health"

const reportMDFile = "report.md"

//go:embed static
var staticFiles embed.FS

// securityHeaders lock the page down: no inline script, no framing, no sniffing.
var securityHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
	"X-Content-Type-Options":  "nosniff",
	"Referrer-Policy":         "strict-origin-when-cross-origin",
	"Permissions-Policy":      "camera=(), microphone=(), geolocation=()",
}

// LiveFunc returns the auditor's latest reading, and false before the first.
type LiveFunc func() (audit.Reading, bool)

// Config is what the panel reads from.
type Config struct {
	Version   string
	Live      LiveFunc
	RoundsDir string
	Logger    *slog.Logger
}

type healthResponse struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

// NewHandler serves the panel port (8090).
func NewHandler(config Config) http.Handler {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		// The directory is embedded at build time; Sub only fails on a bad name.
		panic(err)
	}

	panel := panelHandlers{config: config}

	mux := http.NewServeMux()
	mux.Handle("GET "+HealthPath, healthHandler{response: healthResponse{Service: "lab", Version: config.Version, Status: "ok"}})
	mux.HandleFunc("GET /api/status", panel.status)
	mux.HandleFunc("GET /rounds/{round}/report.md", panel.report)
	mux.Handle("GET /", http.FileServerFS(static))

	return withSecurityHeaders(mux)
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, value := range securityHeaders {
			w.Header().Set(name, value)
		}

		next.ServeHTTP(w, r)
	})
}

type healthHandler struct {
	response healthResponse
}

func (h healthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.response)
}

type panelHandlers struct {
	config Config
}

func (p panelHandlers) status(w http.ResponseWriter, r *http.Request) {
	reading, hasReading := p.config.Live()
	response := statusResponse{Live: liveStatusOf(reading, hasReading)}

	round, err := latestRound(p.config.RoundsDir)
	if err != nil {
		p.config.Logger.WarnContext(r.Context(), "reading the last round", "error", err)
	}

	response.LastRound = round
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}

// report serves report.md as plain text; the round id is checked against the
// round directory pattern, so the path never leaves the rounds directory.
func (p panelHandlers) report(w http.ResponseWriter, r *http.Request) {
	round := r.PathValue("round")
	if !roundDirPattern.MatchString(round) {
		http.NotFound(w, r)

		return
	}

	content, err := os.ReadFile(filepath.Join(p.config.RoundsDir, round, reportMDFile)) //nolint:gosec // G304: round matched roundDirPattern.
	if err != nil {
		http.NotFound(w, r)

		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	// Served as plain text with nosniff, never rendered as HTML. A failed
	// write means the client went away.
	_, _ = w.Write(content) //nolint:gosec // G705: text/plain plus X-Content-Type-Options: nosniff.
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// An encode error here means the client is gone; there is nothing left to report.
	_ = json.NewEncoder(w).Encode(body)
}
