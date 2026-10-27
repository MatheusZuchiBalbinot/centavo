package dashboard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"centavo/lab/internal/audit"
	"centavo/lab/internal/dashboard"
)

type healthBody struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

func TestHealth_ReportsVersion(t *testing.T) {
	t.Parallel()

	handler := dashboard.NewHandler(dashboard.Config{Version: "test"})
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, dashboard.HealthPath, nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusOK)
	}

	var body healthBody

	err := json.Unmarshal(recorder.Body.Bytes(), &body)
	if err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	isExpectedBody := body.Service == "lab" && body.Version == "test" && body.Status == "ok"
	if !isExpectedBody {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestReport_ServesOnlyRoundDirectories(t *testing.T) {
	t.Parallel()

	roundsDir := t.TempDir()
	writeRound(t, roundsDir, "r0001", `{}`)
	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir})

	cases := []struct {
		path string
		want int
	}{
		{path: "/rounds/r0001/report.md", want: http.StatusOK},
		{path: "/rounds/r0002/report.md", want: http.StatusNotFound},
		{path: "/rounds/..%2F..%2Fetc/report.md", want: http.StatusNotFound},
		{path: "/rounds/notes/report.md", want: http.StatusNotFound},
	}

	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))

		if recorder.Code != tc.want {
			t.Errorf("%s: status %d, want %d", tc.path, recorder.Code, tc.want)
		}
	}
}

func TestPanel_SendsSecurityHeadersAndThePage(t *testing.T) {
	t.Parallel()

	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: t.TempDir()})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	csp := recorder.Header().Get("Content-Security-Policy")
	hasLockdown := strings.Contains(csp, "script-src 'self'") && strings.Contains(csp, "frame-ancestors 'none'") && recorder.Header().Get("X-Content-Type-Options") == "nosniff"
	if !hasLockdown {
		t.Fatalf("security headers: %v", recorder.Header())
	}

	if !strings.Contains(recorder.Body.String(), `<html lang="pt-BR">`) {
		t.Fatalf("page body: %s", recorder.Body)
	}
}

func noReading() (audit.Reading, bool) {
	return audit.Reading{}, false
}

// An empty report leaves it unverified.
func writeRound(t *testing.T, roundsDir, round, report string) {
	t.Helper()

	dir := filepath.Join(roundsDir, round)

	err := os.MkdirAll(dir, 0o750)
	if err != nil {
		t.Fatal(err)
	}

	if report == "" {
		return
	}

	for name, content := range map[string]string{"report.json": report, "report.md": "# report"} {
		err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPanel_ServesThePageForAppRoutesButNotForMissingFiles(t *testing.T) {
	t.Parallel()

	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: t.TempDir()})

	testCases := []struct {
		path        string
		status      int
		contentType string
	}{
		{"/", http.StatusOK, "text/html"},
		{"/rounds/r0004", http.StatusOK, "text/html"},
		{"/assets/missing.js", http.StatusNotFound, "text/plain"},
		{"/api/nothing-here", http.StatusNotFound, "application/json"},
	}

	for _, tc := range testCases {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, nil))

		isExpected := recorder.Code == tc.status && strings.HasPrefix(recorder.Header().Get("Content-Type"), tc.contentType)
		if !isExpected {
			t.Fatalf("%s: %d %s, want %d %s", tc.path, recorder.Code, recorder.Header().Get("Content-Type"), tc.status, tc.contentType)
		}
	}
}
