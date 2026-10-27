package dashboard_test

import (
	"cmp"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"centavo/lab/internal/attacks"
	"centavo/lab/internal/dashboard"
	"centavo/lab/internal/manual"
	"centavo/lab/internal/runner"
)

const (
	panelHost  = "lab.test"
	panelURL   = "http://" + panelHost
	smokeStart = `{"attack":"calm","duration_seconds":180}`
	// startsPerHour and attacksPerMinute mirror the panel's limits.
	startsPerHour    = 6
	attacksPerMinute = 12
)

// isSingleRound refuses a second round like the real one; otherwise every
// start succeeds, so a test can reach the rate limit.
type fakeRunner struct {
	isSingleRound bool

	mu       sync.Mutex
	state    runner.State
	requests []runner.Request
}

func (f *fakeRunner) Start(request runner.Request) (runner.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	isBusy := f.isSingleRound && f.state.Status == runner.StatusRunning
	if isBusy {
		return f.state, runner.ErrBusy
	}

	f.requests = append(f.requests, request)
	f.state = runner.State{Status: runner.StatusRunning, Request: request, StartedAt: time.Now()}

	return f.state, nil
}

func (f *fakeRunner) Stop() (runner.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.state.Status != runner.StatusRunning {
		return f.state, runner.ErrIdle
	}

	f.state.Status = runner.StatusStopping

	return f.state, nil
}

func (f *fakeRunner) State() runner.State {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.state
}

type errorResponse struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

type labState struct {
	Status          string `json:"status"`
	Attack          string `json:"attack"`
	Scenario        string `json:"scenario"`
	DurationSeconds int    `json:"duration_seconds"`
}

type postRequest struct {
	path        string
	body        string
	contentType string
	origin      string
}

func newControlPanel(t *testing.T, rounds *fakeRunner) http.Handler {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: t.TempDir(), Logger: logger, Runner: rounds})
}

func post(t *testing.T, handler http.Handler, request postRequest) *httptest.ResponseRecorder {
	t.Helper()

	httpRequest := httptest.NewRequestWithContext(t.Context(), http.MethodPost, panelURL+request.path, strings.NewReader(request.body))

	if request.contentType != "" {
		httpRequest.Header.Set("Content-Type", request.contentType)
	}

	if request.origin != "" {
		httpRequest.Header.Set("Origin", request.origin)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httpRequest)

	return recorder
}

func startRound(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	request := postRequest{path: "/api/rounds", body: body, contentType: "application/json", origin: panelURL}

	return post(t, handler, request)
}

func errorCodeOf(t *testing.T, recorder *httptest.ResponseRecorder) string {
	t.Helper()

	var body errorResponse

	err := json.Unmarshal(recorder.Body.Bytes(), &body)
	if err != nil {
		t.Fatalf("error body %q: %v", recorder.Body, err)
	}

	return body.Error.Code
}

func TestStart_PicksAScenarioOfTheAttackAndRefusesASecondRound(t *testing.T) {
	t.Parallel()

	rounds := &fakeRunner{isSingleRound: true}
	handler := newControlPanel(t, rounds)

	recorder := startRound(t, handler, `{"attack":"clock","duration_seconds":300}`)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", recorder.Code, recorder.Body)
	}

	var state labState

	err := json.Unmarshal(recorder.Body.Bytes(), &state)
	if err != nil {
		t.Fatal(err)
	}

	clockScenarios := attacks.Menu()[slices.IndexFunc(attacks.Menu(), isClock)].Scenarios
	isClockRound := state.Status == "running" && state.Attack == "clock" && slices.Contains(clockScenarios, state.Scenario) && state.DurationSeconds == 300

	if !isClockRound {
		t.Fatalf("lab after start: %+v", state)
	}

	recorder = startRound(t, handler, smokeStart)

	isRefused := recorder.Code == http.StatusConflict && errorCodeOf(t, recorder) == "round_running"
	if !isRefused {
		t.Fatalf("second start: %d %s", recorder.Code, recorder.Body)
	}
}

func isClock(attack attacks.Attack) bool {
	return attack.ID == attacks.Clock
}

func TestStart_RefusesWhatIsNotAValidRequest(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		request postRequest
		status  int
		code    string
	}{
		{"unknown attack", postRequest{body: `{"attack":"drop-tables","duration_seconds":180}`}, http.StatusUnprocessableEntity, "unknown_attack"},
		{"duration not offered", postRequest{body: `{"attack":"calm","duration_seconds":7}`}, http.StatusUnprocessableEntity, "invalid_duration"},
		{"unknown field", postRequest{body: `{"attack":"calm","duration_seconds":180,"defenses":"naive"}`}, http.StatusBadRequest, "invalid_request"},
		{"trailing data", postRequest{body: smokeStart + smokeStart}, http.StatusBadRequest, "invalid_request"},
		{"body too large", postRequest{body: `{"attack":"` + strings.Repeat("x", 2048) + `"}`}, http.StatusRequestEntityTooLarge, "body_too_large"},
		{"form instead of JSON", postRequest{body: smokeStart, contentType: "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType, "unsupported_media_type"},
		{"another site", postRequest{body: smokeStart, origin: "https://evil.test"}, http.StatusForbidden, "forbidden_origin"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rounds := &fakeRunner{isSingleRound: true}
			request := tc.request
			request.path = "/api/rounds"
			request.contentType = cmp.Or(request.contentType, "application/json")
			request.origin = cmp.Or(request.origin, panelURL)

			recorder := post(t, newControlPanel(t, rounds), request)

			isExpected := recorder.Code == tc.status && errorCodeOf(t, recorder) == tc.code
			if !isExpected {
				t.Fatalf("got %d %s, want %d %s", recorder.Code, recorder.Body, tc.status, tc.code)
			}

			if len(rounds.requests) != 0 {
				t.Fatalf("a refused request started a round: %+v", rounds.requests)
			}
		})
	}
}

func TestStart_LimitsTheRoundsPerHour(t *testing.T) {
	t.Parallel()

	handler := newControlPanel(t, &fakeRunner{})

	for start := range startsPerHour {
		recorder := startRound(t, handler, smokeStart)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("start %d: %d %s", start+1, recorder.Code, recorder.Body)
		}
	}

	recorder := startRound(t, handler, smokeStart)

	isLimited := recorder.Code == http.StatusTooManyRequests && errorCodeOf(t, recorder) == "rate_limited" && recorder.Header().Get("Retry-After") != ""
	if !isLimited {
		t.Fatalf("start past the limit: %d %s", recorder.Code, recorder.Body)
	}
}

func TestStop_EndsTheRunningRoundAndRefusesWhenIdle(t *testing.T) {
	t.Parallel()

	handler := newControlPanel(t, &fakeRunner{isSingleRound: true})
	stop := postRequest{path: "/api/lab/stop", origin: panelURL}

	recorder := post(t, handler, stop)

	isRefused := recorder.Code == http.StatusConflict && errorCodeOf(t, recorder) == "lab_idle"
	if !isRefused {
		t.Fatalf("stop when idle: %d %s", recorder.Code, recorder.Body)
	}

	startRound(t, handler, smokeStart)

	recorder = post(t, handler, stop)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("stop: %d %s", recorder.Code, recorder.Body)
	}

	var state labState

	code := get(t, handler, "/api/lab", &state)

	isStopping := code == http.StatusOK && state.Status == "stopping"
	if !isStopping {
		t.Fatalf("lab after stop: %d %+v", code, state)
	}
}

func TestStop_RefusesAnotherSite(t *testing.T) {
	t.Parallel()

	rounds := &fakeRunner{isSingleRound: true}
	handler := newControlPanel(t, rounds)
	startRound(t, handler, smokeStart)

	recorder := post(t, handler, postRequest{path: "/api/lab/stop", origin: "https://evil.test"})

	isRefused := recorder.Code == http.StatusForbidden && rounds.State().Status == runner.StatusRunning
	if !isRefused {
		t.Fatalf("stop from another site: %d, lab %s", recorder.Code, rounds.State().Status)
	}
}

func TestRounds_PagesTheFinishedRoundsNewestFirst(t *testing.T) {
	t.Parallel()

	roundsDir := t.TempDir()

	for _, round := range []string{"r0001", "r0002", "r0003"} {
		writeLiveRound(t, roundsDir, round, `{"verdict":"pass","invariants":[]}`)
	}

	// Still running: no report yet, so it is not listed.
	writeLiveRound(t, roundsDir, "r0004", "")

	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir})

	type page struct {
		Rounds []struct {
			Round string `json:"round"`
		} `json:"rounds"`
		NextCursor string `json:"next_cursor"`
	}

	var first page

	get(t, handler, "/api/rounds?limit=2", &first)

	isFirstPage := len(first.Rounds) == 2 && first.Rounds[0].Round == "r0003" && first.Rounds[1].Round == "r0002" && first.NextCursor == "r0002"
	if !isFirstPage {
		t.Fatalf("first page: %+v", first)
	}

	var second page

	get(t, handler, "/api/rounds?limit=2&cursor="+first.NextCursor, &second)

	isLastPage := len(second.Rounds) == 1 && second.Rounds[0].Round == "r0001" && second.NextCursor == ""
	if !isLastPage {
		t.Fatalf("second page: %+v", second)
	}

	for _, query := range []string{"limit=0", "limit=101", "limit=x", "cursor=../etc"} {
		code := get(t, handler, "/api/rounds?"+query, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400", query, code)
		}
	}
}

func TestLab_NamesTheRoundOnceItHasStarted(t *testing.T) {
	t.Parallel()

	roundsDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rounds := &fakeRunner{isSingleRound: true}
	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir, Logger: logger, Runner: rounds})

	// A round from before the panel's start is not this one.
	writeLiveRound(t, roundsDir, "r0001", `{"verdict":"pass","invariants":[]}`)
	startRound(t, handler, smokeStart)

	type lab struct {
		Status string `json:"status"`
		Round  string `json:"round"`
	}

	var before lab

	get(t, handler, "/api/lab", &before)

	if before.Round != "" {
		t.Fatalf("named %q before the round wrote round.json", before.Round)
	}

	startedAt := rounds.State().StartedAt.UTC().Format(time.RFC3339Nano)
	writeRound(t, roundsDir, "r0002", "")

	err := os.WriteFile(filepath.Join(roundsDir, "r0002", "round.json"), []byte(`{"round":"r0002","started_at":"`+startedAt+`"}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	var after lab

	get(t, handler, "/api/lab", &after)

	isNamed := after.Status == "running" && after.Round == "r0002"
	if !isNamed {
		t.Fatalf("lab after the round started: %+v", after)
	}
}

func TestAttack_ReachesTheRoundOnlyDuringItsAttackPhase(t *testing.T) {
	t.Parallel()

	roundsDir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rounds := &fakeRunner{isSingleRound: true}
	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir, Logger: logger, Runner: rounds})
	kill := postRequest{path: "/api/lab/attacks", body: `{"action":"kill","target":"bank-worker-1"}`, contentType: "application/json", origin: panelURL}

	recorder := post(t, handler, kill)

	isIdle := recorder.Code == http.StatusConflict && errorCodeOf(t, recorder) == "lab_idle"
	if !isIdle {
		t.Fatalf("attack with no round: %d %s", recorder.Code, recorder.Body)
	}

	startRound(t, handler, smokeStart)
	startNamedRound(t, roundsDir, "r0007", rounds.State().StartedAt)

	recorder = post(t, handler, kill)

	isBeforeTheAttack := recorder.Code == http.StatusConflict && errorCodeOf(t, recorder) == "attack_closed"
	if !isBeforeTheAttack {
		t.Fatalf("attack before the attack phase: %d %s", recorder.Code, recorder.Body)
	}

	commandsDir := filepath.Join(roundsDir, "r0007", manual.DirName)

	err := manual.Open(commandsDir)
	if err != nil {
		t.Fatal(err)
	}

	recorder = post(t, handler, kill)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("attack: %d %s", recorder.Code, recorder.Body)
	}

	commands, err := manual.Take(commandsDir)

	isDelivered := err == nil && slices.Equal(commands, []manual.Command{{Action: manual.Kill, Target: "bank-worker-1"}})
	if !isDelivered {
		t.Fatalf("commands %+v, %v", commands, err)
	}

	_, err = rounds.Stop()
	if err != nil {
		t.Fatal(err)
	}

	recorder = post(t, handler, kill)

	isAfterTheStop := recorder.Code == http.StatusConflict && errorCodeOf(t, recorder) == "attack_closed"
	if !isAfterTheStop {
		t.Fatalf("attack after the stop: %d %s", recorder.Code, recorder.Body)
	}
}

func TestAttack_RefusesWhatThePanelDoesNotOffer(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		request postRequest
		status  int
		code    string
	}{
		{"kill a link", postRequest{body: `{"action":"kill","target":"api_pg"}`}, http.StatusUnprocessableEntity, "unknown_attack"},
		{"cut a process", postRequest{body: `{"action":"cut","target":"postgres"}`}, http.StatusUnprocessableEntity, "unknown_attack"},
		{"the auditor", postRequest{body: `{"action":"kill","target":"auditor"}`}, http.StatusUnprocessableEntity, "unknown_attack"},
		{"unknown field", postRequest{body: `{"action":"kill","target":"partner","seconds":600}`}, http.StatusBadRequest, "invalid_request"},
		{"another site", postRequest{body: `{"action":"kill","target":"partner"}`, origin: "https://evil.test"}, http.StatusForbidden, "forbidden_origin"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			roundsDir := t.TempDir()
			rounds := &fakeRunner{isSingleRound: true}
			handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Runner: rounds})
			commandsDir := underAttack(t, handler, rounds, roundsDir)

			request := tc.request
			request.path = "/api/lab/attacks"
			request.contentType = "application/json"
			request.origin = cmp.Or(request.origin, panelURL)

			recorder := post(t, handler, request)

			isExpected := recorder.Code == tc.status && errorCodeOf(t, recorder) == tc.code
			if !isExpected {
				t.Fatalf("got %d %s, want %d %s", recorder.Code, recorder.Body, tc.status, tc.code)
			}

			commands, _ := manual.Take(commandsDir)
			if len(commands) != 0 {
				t.Fatalf("a refused attack reached the round: %+v", commands)
			}
		})
	}
}

func TestAttack_LimitsTheAttacksPerMinute(t *testing.T) {
	t.Parallel()

	roundsDir := t.TempDir()
	rounds := &fakeRunner{isSingleRound: true}
	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Runner: rounds})
	underAttack(t, handler, rounds, roundsDir)

	slow := postRequest{path: "/api/lab/attacks", body: `{"action":"slow","target":"client_api"}`, contentType: "application/json", origin: panelURL}

	for attack := range attacksPerMinute {
		recorder := post(t, handler, slow)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("attack %d: %d %s", attack+1, recorder.Code, recorder.Body)
		}
	}

	recorder := post(t, handler, slow)

	isLimited := recorder.Code == http.StatusTooManyRequests && errorCodeOf(t, recorder) == "rate_limited" && recorder.Header().Get("Retry-After") != ""
	if !isLimited {
		t.Fatalf("attack past the limit: %d %s", recorder.Code, recorder.Body)
	}
}

// underAttack starts a round and opens its attack phase, returning the
// round's commands directory.
func underAttack(t *testing.T, handler http.Handler, rounds *fakeRunner, roundsDir string) string {
	t.Helper()

	startRound(t, handler, smokeStart)
	startNamedRound(t, roundsDir, "r0001", rounds.State().StartedAt)

	commandsDir := filepath.Join(roundsDir, "r0001", manual.DirName)

	err := manual.Open(commandsDir)
	if err != nil {
		t.Fatal(err)
	}

	return commandsDir
}

func startNamedRound(t *testing.T, roundsDir, round string, startedAt time.Time) {
	t.Helper()

	writeRound(t, roundsDir, round, "")

	manifest := `{"round":"` + round + `","started_at":"` + startedAt.UTC().Format(time.RFC3339Nano) + `"}`

	err := os.WriteFile(filepath.Join(roundsDir, round, "round.json"), []byte(manifest), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}
