package dashboard

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"centavo/lab/internal/audit"
	"centavo/lab/internal/replay"
)

const HealthPath = "/internal/health"

const reportMDFile = "report.md"

// securityHeaders lock the page down: no inline script, no framing, no sniffing.
var securityHeaders = map[string]string{
	"Content-Security-Policy": "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; font-src 'self'; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
	"X-Content-Type-Options": "nosniff", "Referrer-Policy": "strict-origin-when-cross-origin",
	"Permissions-Policy": "camera=(), microphone=(), geolocation=()",
}

type LiveFunc func() (audit.Reading, bool)

type Config struct {
	Version   string
	Live      LiveFunc
	RoundsDir string
	Logger    *slog.Logger
	// Without one, the panel only reads.
	Runner RoundRunner
}

type healthResponse struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

type healthHandler struct {
	response healthResponse
}

type panelHandlers struct {
	config  Config
	tracker *tracker
	streams chan struct{}
}

func NewHandler(config Config) http.Handler {
	panel := panelHandlers{config: config, tracker: newTracker(config.RoundsDir), streams: make(chan struct{}, maxStreams)}

	mux := http.NewServeMux()
	mux.Handle("GET "+HealthPath, healthHandler{response: healthResponse{Service: "lab", Version: config.Version, Status: "ok"}})
	mux.HandleFunc("GET "+replayRoute, panel.replay)
	mux.HandleFunc("GET "+reportRoute, panel.report)
	mux.Handle("GET "+staticRoute, newAppHandler())
	mux.HandleFunc("GET "+apiPrefix, panel.apiNotFound)
	mux.HandleFunc("GET "+roundsRoute, panel.rounds)
	mux.HandleFunc("GET "+eventsRoute, panel.events)

	if config.Runner != nil {
		handleControl(mux, control{panel: panel, runner: config.Runner, limiter: newWindowLimiter(roundStartLimit)})
	}

	return withSecurityHeaders(mux)
}

func handleControl(mux *http.ServeMux, routes control) {
	mux.HandleFunc("GET "+labRoute, routes.lab)
	mux.HandleFunc("GET "+attacksRoute, routes.menu)
	mux.HandleFunc("POST "+roundsRoute, routes.start)
	mux.HandleFunc("POST "+stopRoute, routes.stop)
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for name, value := range securityHeaders {
			w.Header().Set(name, value)
		}

		next.ServeHTTP(w, r)
	})
}

func (h healthHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.response)
}

// The cursor is the last round of the previous page.
func (p panelHandlers) rounds(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	cursor := query.Get("cursor")

	size, err := pageSizeOf(query.Get("limit"))
	if err != nil {
		p.writeError(w, r, err)

		return
	}

	hasBadCursor := cursor != "" && !roundDirPattern.MatchString(cursor)
	if hasBadCursor {
		p.writeError(w, r, errInvalidRequest)

		return
	}

	page, err := p.tracker.page(cursor, size)
	if err != nil {
		p.writeError(w, r, err)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}

func (p panelHandlers) replay(w http.ResponseWriter, r *http.Request) {
	round := r.PathValue("round")
	if !roundDirPattern.MatchString(round) {
		p.writeError(w, r, errNotFound)

		return
	}

	built, err := replay.FromRound(filepath.Join(p.config.RoundsDir, round))
	if err != nil {
		p.writeError(w, r, errNotFound)

		return
	}

	writeJSON(w, http.StatusOK, built)
}

// The round id is checked against the round directory pattern, so the path
// never leaves the rounds directory.
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
