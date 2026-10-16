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

type fakePinger struct {
	err error
}

func (f fakePinger) Ping(context.Context) error {
	return f.err
}

type healthBody struct {
	Service    string            `json:"service"`
	ChaosBuild bool              `json:"chaos_build"`
	Defenses   string            `json:"defenses"`
	Status     string            `json:"status"`
	Checks     map[string]string `json:"checks"`
}

var testBuildInfo = httpapi.BuildInfo{Service: "bank-api", Version: "test", ChaosBuild: true, Env: "chaoslab", Defenses: "robust"}

func TestHealth_DatabaseReachable_ReturnsOK(t *testing.T) {
	handler := httpapi.NewAdminHandler(testBuildInfo, fakePinger{})

	recorder, body := getHealth(t, handler)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", recorder.Code, http.StatusOK)
	}

	isExpectedBody := body.Status == "ok" && body.Checks["database"] == "ok" && body.ChaosBuild && body.Defenses == "robust" && body.Service == "bank-api"
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
