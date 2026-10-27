package dashboard_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"centavo/lab/internal/dashboard"
)

var historyLines = strings.Join([]string{
	`{"index":0,"round":"r0001","time_ns":100000000,"wall":"x","process":1,"type":"invoke","f":"deposit","op":"r0001-p01-000001"}`,
	`{"index":1,"round":"r0001","time_ns":300000000,"wall":"x","process":1,"type":"ok","f":"deposit","op":"r0001-p01-000001"}`,
	`{"index":2,"round":"r0001","time_ns":1500000000,"wall":"x","process":"nemesis","type":"info","f":"kill","value":{"target":"bank-api"}}`,
}, "\n") + "\n"

func TestReplay_ServesOnlyRoundDirectories(t *testing.T) {
	t.Parallel()

	roundsDir := t.TempDir()
	writeLiveRound(t, roundsDir, "r0003", `{"verdict":"pass","invariants":[{"id":"I1","status":"ok"}]}`)
	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir})

	var replay struct {
		Verdict string `json:"verdict"`
	}

	code := get(t, handler, "/api/replay/r0003", &replay)

	isUnexpected := code != http.StatusOK || replay.Verdict != "pass"
	if isUnexpected {
		t.Fatalf("replay of r0003: %d, %+v", code, replay)
	}

	for _, path := range []string{"/api/replay/r..0003", "/api/replay/r0004", "/api/replay/etc"} {
		if code := get(t, handler, path, nil); code != http.StatusNotFound {
			t.Fatalf("%s answered %d", path, code)
		}
	}
}

func writeLiveRound(t *testing.T, roundsDir, round, report string) {
	t.Helper()

	writeRound(t, roundsDir, round, report)
	dir := filepath.Join(roundsDir, round)

	files := map[string]string{
		"round.json": `{"round":"` + round + `","scenario":"smoke","seed":"0x5eed"}`, "history.jsonl": strings.ReplaceAll(historyLines, "r0001", round),
	}

	for name, content := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func get(t *testing.T, handler http.Handler, path string, into any) int {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

	shouldDecode := into != nil && recorder.Code == http.StatusOK
	if shouldDecode {
		err := json.Unmarshal(recorder.Body.Bytes(), into)
		if err != nil {
			t.Fatalf("%s body %q: %v", path, recorder.Body, err)
		}
	}

	return recorder.Code
}

func appendTo(t *testing.T, path, content string) {
	t.Helper()

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	_, err = file.WriteString(content)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRounds_ListsEachRoundsDefenseProfile(t *testing.T) {
	t.Parallel()

	roundsDir := t.TempDir()
	writeLiveRound(t, roundsDir, "r0005", `{"verdict":"fail","invariants":[{"id":"I1","status":"failed"}]}`)

	manifest := `{"round":"r0005","scenario":"smoke","seed":"0x5eed","defenses":"robust,-fencing"}`

	err := os.WriteFile(filepath.Join(roundsDir, "r0005", "round.json"), []byte(manifest), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir})

	var page struct {
		Rounds []struct {
			Round    string `json:"round"`
			Defenses string `json:"defenses"`
		} `json:"rounds"`
	}

	code := get(t, handler, "/api/rounds?limit=10", &page)

	isUnexpected := code != http.StatusOK || len(page.Rounds) != 1 || page.Rounds[0].Defenses != "robust,-fencing"
	if isUnexpected {
		t.Fatalf("rounds: %d, %+v", code, page)
	}
}
