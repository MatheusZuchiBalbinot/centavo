package dashboard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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

func TestStatus_PaintsTheLiveChecks(t *testing.T) {
	t.Parallel()

	negative := int64(-2_000)
	zero := int64(0)

	cases := []struct {
		name       string
		reading    audit.Reading
		hasReading bool
		want       []string
	}{
		{name: "before the first reading", want: []string{"unknown", "unknown"}},
		{name: "database down", reading: audit.Reading{IsUnavailable: true}, hasReading: true, want: []string{"unknown", "unknown"}},
		{name: "healthy", reading: audit.Reading{SumCents: 0, MinCustomerCents: &zero}, hasReading: true, want: []string{"ok", "ok"}},
		{name: "money created and overdraft", reading: audit.Reading{SumCents: 150, MinCustomerCents: &negative}, hasReading: true, want: []string{"violated", "violated"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			live := func() (audit.Reading, bool) { return tc.reading, tc.hasReading }
			status := getStatus(t, dashboard.Config{Live: live, RoundsDir: t.TempDir()})

			got := []string{status.Live.Checks[0].State, status.Live.Checks[1].State}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("states %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStatus_ShowsTheNewestVerifiedRound(t *testing.T) {
	t.Parallel()

	roundsDir := t.TempDir()
	writeRound(t, roundsDir, "r0009", `{"round": "r0009", "verdict": "pass"}`)
	writeRound(t, roundsDir, "r0010", `{"round": "r0010", "verdict": "fail"}`)
	writeRound(t, roundsDir, "r0011", "")

	status := getStatus(t, dashboard.Config{Live: noReading, RoundsDir: roundsDir})

	isNewestWithReport := status.LastRound != nil && status.LastRound.Round == "r0010" && status.LastRound.Verdict == "fail" && status.LastRound.ReportURL == "/rounds/r0010/report.md"
	if !isNewestWithReport {
		t.Fatalf("last round: %+v", status.LastRound)
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

type statusBody struct {
	Live struct {
		Checks []struct {
			State string `json:"state"`
		} `json:"checks"`
	} `json:"live"`
	LastRound *struct {
		Round     string `json:"round"`
		Verdict   string `json:"verdict"`
		ReportURL string `json:"report_url"`
	} `json:"last_round"`
}

func getStatus(t *testing.T, config dashboard.Config) statusBody {
	t.Helper()

	recorder := httptest.NewRecorder()
	dashboard.NewHandler(config).ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/status", nil))

	var body statusBody

	err := json.Unmarshal(recorder.Body.Bytes(), &body)
	if err != nil {
		t.Fatalf("status body %q: %v", recorder.Body, err)
	}

	return body
}

func noReading() (audit.Reading, bool) {
	return audit.Reading{}, false
}

// writeRound creates a round directory; an empty report leaves it unverified.
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
