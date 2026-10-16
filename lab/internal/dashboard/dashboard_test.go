package dashboard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"centavo/lab/internal/dashboard"
)

type healthBody struct {
	Service string `json:"service"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

func TestHealth_ReportsVersion(t *testing.T) {
	handler := dashboard.NewHandler("test")
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
