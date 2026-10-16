package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"centavo/partner/internal/interfaces/httpapi"
)

type healthBody struct {
	Service    string `json:"service"`
	ChaosBuild bool   `json:"chaos_build"`
	Env        string `json:"env"`
	Status     string `json:"status"`
}

func TestHealth_ReportsBuild(t *testing.T) {
	info := httpapi.BuildInfo{Version: "test", ChaosBuild: true, Env: "chaoslab"}
	handler := httpapi.NewAdminHandler(info, httpapi.API{}, nil)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, httpapi.HealthPath, nil)
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

	isExpectedBody := body.Service == "partner" && body.ChaosBuild && body.Env == "chaoslab" && body.Status == "ok"
	if !isExpectedBody {
		t.Fatalf("unexpected body: %+v", body)
	}
}
