package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"centavo/bank/internal/interfaces/httpapi"
)

var testBuildInfo = httpapi.BuildInfo{Service: "bank-api", Version: "test", IsChaosBuild: true, Env: "chaoslab", Defenses: "robust"}

type fakePinger struct {
	err error
}

type healthBody struct {
	Service      string            `json:"service"`
	IsChaosBuild bool              `json:"chaos_build"`
	Defenses     string            `json:"defenses"`
	Status       string            `json:"status"`
	Checks       map[string]string `json:"checks"`
}

func TestHealth_DatabaseReachable_ReturnsOK(t *testing.T) {
	handler := httpapi.NewAdminHandler(testBuildInfo, fakePinger{})

	recorder, body := getHealth(t, handler)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusOK)
	}

	isExpectedBody := body.Status == "ok" && body.Checks["database"] == "ok" && body.IsChaosBuild && body.Defenses == "robust" && body.Service == "bank-api"
	if !isExpectedBody {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestHealth_DatabaseUnreachable_ReturnsUnavailable(t *testing.T) {
	pinger := fakePinger{err: errors.New("connection refused")}
	handler := httpapi.NewAdminHandler(testBuildInfo, pinger)

	recorder, body := getHealth(t, handler)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}

	isExpectedBody := body.Status == "unavailable" && body.Checks["database"] == "error"
	if !isExpectedBody {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestHealth_RejectsOtherMethods(t *testing.T) {
	handler := httpapi.NewAdminHandler(testBuildInfo, fakePinger{})
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, httpapi.HealthPath, nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

// TestHealth_SendsSecurityHeaders: the admin port, like the client one,
// answers with headers that keep a browser from rendering or sniffing it.
func TestHealth_SendsSecurityHeaders(t *testing.T) {
	recorder, _ := getHealth(t, httpapi.NewAdminHandler(testBuildInfo, fakePinger{}))

	isLockedDown := recorder.Header().Get("X-Content-Type-Options") == "nosniff" && recorder.Header().Get("Content-Security-Policy") != ""
	if !isLockedDown {
		t.Fatalf("headers %v", recorder.Header())
	}
}

func (f fakePinger) Ping(context.Context) error {
	return f.err
}

func getHealth(t *testing.T, handler http.Handler) (*httptest.ResponseRecorder, healthBody) {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, httpapi.HealthPath, nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	var body healthBody

	err := json.Unmarshal(recorder.Body.Bytes(), &body)
	if err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	return recorder, body
}
