package httpapi_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"centavo/partner/internal/engine"
	"centavo/partner/internal/interfaces/httpapi"
	"centavo/partner/internal/modes"
	"centavo/partner/internal/storage"
	"centavo/partner/internal/truth"
)

const participant = "centavo-robust"

type partnerFixture struct {
	api      http.Handler
	admin    http.Handler
	store    *storage.DB
	registry *modes.Registry
}

func TestSubmit_IsIdempotentByEndToEndID(t *testing.T) {
	fixture := newPartner(t)
	e2e := "E0192f1c5a1b2c3d4"

	first := call(t, fixture.api, http.MethodPost, "/v1/transfers", transferBody(e2e, "dest-0001"))
	again := call(t, fixture.api, http.MethodPost, "/v1/transfers", transferBody(e2e, "dest-0001"))

	isIdempotent := first.Code == http.StatusAccepted && again.Code == http.StatusOK && strings.Contains(again.Body.String(), `"status":"processing"`)
	if !isIdempotent {
		t.Fatalf("first %d %s, again %d %s", first.Code, first.Body, again.Code, again.Body)
	}

	status := call(t, fixture.api, http.MethodGet, "/v1/transfers/"+e2e, "")
	missing := call(t, fixture.api, http.MethodGet, "/v1/transfers/E00000000deadbeef", "")

	isUnexpected := status.Code != http.StatusOK || missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "not_found")
	if isUnexpected {
		t.Fatalf("status %d, missing %d %s", status.Code, missing.Code, missing.Body)
	}
}

func TestSubmit_RefusesUnknownDestinationKeys(t *testing.T) {
	fixture := newPartner(t)

	refused := call(t, fixture.api, http.MethodPost, "/v1/transfers", transferBody("E0192f1c5a1b2c3d5", "invalid-0001"))
	isUnexpected := refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), `"status":"rejected"`)
	if isUnexpected {
		t.Fatalf("got %d %s", refused.Code, refused.Body)
	}

	malformed := call(t, fixture.api, http.MethodPost, "/v1/transfers", `{"end_to_end_id": "x"}`)
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed: %d", malformed.Code)
	}
}

func TestOutage_AnswersServiceUnavailable(t *testing.T) {
	fixture := newPartner(t)

	err := fixture.registry.Set(modes.Config{ParticipantID: participant, TTL: "1m", Outage: &modes.OutageConfig{Until: "+1m", Mode: modes.OutageServiceUnavailable}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	response := call(t, fixture.api, http.MethodPost, "/v1/transfers", transferBody("E0192f1c5a1b2c3d6", "dest-0001"))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d", response.Code)
	}

	fixture.registry.Clear()

	response = call(t, fixture.api, http.MethodPost, "/v1/transfers", transferBody("E0192f1c5a1b2c3d6", "dest-0001"))
	if response.Code != http.StatusAccepted {
		t.Fatalf("after the outage: %d (the refused submission must not have been recorded)", response.Code)
	}
}

func TestIncomingAndTruth_ShowTheStatementTheBankReconcilesAgainst(t *testing.T) {
	fixture := newPartner(t)

	created := call(t, fixture.admin, http.MethodPost, "/internal/incoming", `{"participant_id": "`+participant+`", "end_to_end_id": "E0192f1c5a1b2c3d7", "account_id": "acc-1", "amount_cents": 900}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("incoming: %d %s", created.Code, created.Body)
	}

	statement := call(t, fixture.api, http.MethodGet, "/v1/statements?participant_id="+participant, "")
	if !strings.Contains(statement.Body.String(), `"kind":"incoming"`) {
		t.Fatalf("statement: %s", statement.Body)
	}

	var export truth.Export

	err := json.Unmarshal(call(t, fixture.admin, http.MethodGet, "/internal/truth?participant_id="+participant, "").Body.Bytes(), &export)
	if err != nil {
		t.Fatalf("truth: %+v, %v", export, err)
	}

	isUnexpected := len(export.Incoming) != 1 || len(export.Statement) != 1
	if isUnexpected {
		t.Fatalf("truth: %+v, %v", export, err)
	}

	unknown := call(t, fixture.api, http.MethodGet, "/v1/statements?participant_id=someone-else", "")
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("statement of an unknown participant: %d", unknown.Code)
	}
}

// TestEngine_SettlesAndDeliversSignedWebhooks runs the partner end to end:
// submit, settle by the engine, webhook to the bank with a valid signature.
func TestEngine_SettlesAndDeliversSignedWebhooks(t *testing.T) {
	fixture := newPartner(t)
	secret := []byte(strings.Repeat("s", 32))

	var (
		mu       sync.Mutex
		received []map[string]any
	)

	bank := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		if !hasValidSignature(r.Header.Get("X-Partner-Signature"), body, secret) {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		var event map[string]any
		_ = json.Unmarshal(body, &event)

		mu.Lock()
		received = append(received, event)
		mu.Unlock()
	}))
	defer bank.Close()

	worker := engine.New(engine.Config{Queue: fixture.store, Saga: truth.NewSaga(fixture.store), Modes: fixture.registry, Participants: []engine.Participant{{ID: participant, WebhookURL: bank.URL}}, Secret: secret, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	go worker.Run(t.Context())

	call(t, fixture.api, http.MethodPost, "/v1/transfers", transferBody("E0192f1c5a1b2c3d8", "dest-0001"))

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(received)
		mu.Unlock()

		if count > 0 {
			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	isSettled := len(received) == 1 && received[0]["type"] == "transfer.settled" && received[0]["sequence"] == float64(1)
	if !isSettled {
		t.Fatalf("webhooks: %+v", received)
	}
}

// TestEngine_EchoesTheCorrelationIDOnWebhooks guards the lab's failpoints on
// the bank's webhook path: they match the PIX's correlation id, which only
// reaches the bank if the partner sends it back.
func TestEngine_EchoesTheCorrelationIDOnWebhooks(t *testing.T) {
	fixture := newPartner(t)
	secret := []byte(strings.Repeat("s", 32))
	echoed := make(chan string, 2)

	bank := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		echoed <- r.Header.Get("X-Correlation-Id")
	}))
	defer bank.Close()

	worker := engine.New(engine.Config{Queue: fixture.store, Saga: truth.NewSaga(fixture.store), Modes: fixture.registry, Participants: []engine.Participant{{ID: participant, WebhookURL: bank.URL}}, Secret: secret, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	go worker.Run(t.Context())

	submit := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/transfers", strings.NewReader(transferBody("E0192f1c5a1b2c3d9", "dest-0001")))
	submit.Header.Set("X-Correlation-Id", "r0001-p01-000001")
	fixture.api.ServeHTTP(httptest.NewRecorder(), submit)

	incomingBody := `{"participant_id": "` + participant + `", "end_to_end_id": "E0192f1c5a1b2c3da", "account_id": "acc-1", "amount_cents": 900}`
	incoming := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/internal/incoming", strings.NewReader(incomingBody))
	incoming.Header.Set("X-Correlation-Id", "r0001-p02-000001")
	fixture.admin.ServeHTTP(httptest.NewRecorder(), incoming)

	got := map[string]bool{}

	for range 2 {
		select {
		case correlationID := <-echoed:
			got[correlationID] = true
		case <-time.After(5 * time.Second):
			t.Fatalf("webhooks echoed %v before the deadline", got)
		}
	}

	if !got["r0001-p01-000001"] || !got["r0001-p02-000001"] {
		t.Fatalf("echoed correlation ids: %v", got)
	}
}

// TestErrors_AnswerAStableCode: every refusal carries a machine-readable code
// and the rule the request broke, never the decoder's detail.
func TestErrors_AnswerAStableCode(t *testing.T) {
	fixture := newPartner(t)

	cases := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
	}{
		{name: "malformed submit", method: http.MethodPost, path: "/v1/transfers", body: `{"end_to_end_id": 7}`, wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
		{name: "unknown transfer", method: http.MethodGet, path: "/v1/transfers/E00000000deadbeef", wantStatus: http.StatusNotFound, wantCode: "not_found"},
		{name: "bad statement query", method: http.MethodGet, path: "/v1/statements?participant_id=nobody", wantStatus: http.StatusBadRequest, wantCode: "invalid_request"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := call(t, fixture.api, tc.method, tc.path, tc.body)

			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}

			err := json.Unmarshal(recorder.Body.Bytes(), &body)
			if err != nil {
				t.Fatal(err)
			}

			isExpected := recorder.Code == tc.wantStatus && body.Error.Code == tc.wantCode && !strings.Contains(body.Error.Message, "json")
			if !isExpected {
				t.Fatalf("got %d %s", recorder.Code, recorder.Body)
			}
		})
	}
}

func newPartner(t *testing.T) partnerFixture {
	t.Helper()

	store, err := storage.Open(t.Context(), filepath.Join(t.TempDir(), "truth.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	registry := modes.NewRegistry()
	apiConfig := httpapi.APIConfig{Intake: truth.NewIntake(store), Reads: store, Modes: registry, Participants: []string{participant}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	api := httpapi.NewAPI(apiConfig)

	return partnerFixture{api: api.Handler(), admin: httpapi.NewAdminHandler(httpapi.BuildInfo{}, api, nil), store: store, registry: registry}
}

func call(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body)))

	return recorder
}

func transferBody(e2e, key string) string {
	return `{"end_to_end_id": "` + e2e + `", "participant_id": "` + participant + `", "amount_cents": 4500, "destination_key": "` + key + `"}`
}

func hasValidSignature(header string, body, secret []byte) bool {
	timestamp, signature, isFound := strings.Cut(strings.TrimPrefix(header, "t="), ",v1=")
	if !isFound {
		return false
	}

	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(timestamp + "."))
	_, _ = mac.Write(body)

	return hmac.Equal([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(signature))
}
