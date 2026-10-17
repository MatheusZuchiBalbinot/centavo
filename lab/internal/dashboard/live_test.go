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

// historyLines are one deposit and one random kill, as the lab writes them.
var historyLines = strings.Join([]string{
	`{"index":0,"round":"r0001","time_ns":100000000,"wall":"x","process":1,"type":"invoke","f":"deposit","op":"r0001-p01-000001"}`,
	`{"index":1,"round":"r0001","time_ns":300000000,"wall":"x","process":1,"type":"ok","f":"deposit","op":"r0001-p01-000001"}`,
	`{"index":2,"round":"r0001","time_ns":1500000000,"wall":"x","process":"nemesis","type":"info","f":"kill","value":{"target":"bank-api"}}`,
}, "\n") + "\n"

func writeLiveRound(t *testing.T, roundsDir, round, report string) {
	t.Helper()

	writeRound(t, roundsDir, round, report)
	dir := filepath.Join(roundsDir, round)

	files := map[string]string{
		"round.json":    `{"round":"` + round + `","scenario":"smoke","seed":"0x5eed"}`,
		"history.jsonl": strings.ReplaceAll(historyLines, "r0001", round),
	}

	for name, content := range files {
		err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}
}

type liveBody struct {
	Current *struct {
		Round   string `json:"round"`
		Seconds []struct {
			OK int `json:"ok"`
		} `json:"seconds"`
		Events []struct {
			Family string `json:"family"`
		} `json:"events"`
	} `json:"current"`
	Headline struct {
		Kills        int `json:"kills"`
		Rounds       int `json:"rounds"`
		FailedRounds int `json:"failed_rounds"`
	} `json:"headline"`
	Rounds []struct {
		Round      string `json:"round"`
		Operations int    `json:"operations"`
	} `json:"rounds"`
}

func get(t *testing.T, handler http.Handler, path string, into any) int {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))

	if into != nil && recorder.Code == http.StatusOK {
		err := json.Unmarshal(recorder.Body.Bytes(), into)
		if err != nil {
			t.Fatalf("%s body %q: %v", path, recorder.Body, err)
		}
	}

	return recorder.Code
}

func TestLive_FollowsTheCurrentRoundAndAddsUpTheFinishedOnes(t *testing.T) {
	roundsDir := t.TempDir()
	writeLiveRound(t, roundsDir, "r0001", `{"verdict":"fail","invariants":[]}`)
	writeLiveRound(t, roundsDir, "r0002", "")
	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir})

	var live liveBody

	code := get(t, handler, "/api/live", &live)
	if code != http.StatusOK || live.Current == nil {
		t.Fatalf("status %d, current %+v", code, live.Current)
	}

	isCurrent := live.Current.Round == "r0002" && len(live.Current.Seconds) == 1 && live.Current.Seconds[0].OK == 1 && len(live.Current.Events) == 1
	if !isCurrent {
		t.Fatalf("current round: %+v", live.Current)
	}

	isAddedUp := live.Headline.Rounds == 1 && live.Headline.FailedRounds == 1 && live.Headline.Kills == 1 && len(live.Rounds) == 1 && live.Rounds[0].Operations == 1
	if !isAddedUp {
		t.Fatalf("headline %+v, rounds %+v", live.Headline, live.Rounds)
	}

	appendTo(t, filepath.Join(roundsDir, "r0002", "history.jsonl"), `{"index":3,"round":"r0002","time_ns":2100000000,"wall":"x","process":"nemesis","type":"info","f":"heal_start"}`+"\n")
	get(t, handler, "/api/live", &live)

	if len(live.Current.Events) != 2 {
		t.Fatalf("the line appended later was not read: %+v", live.Current.Events)
	}
}

func TestReplay_ServesOnlyRoundDirectories(t *testing.T) {
	roundsDir := t.TempDir()
	writeLiveRound(t, roundsDir, "r0003", `{"verdict":"pass","invariants":[{"id":"I1","status":"ok"}]}`)
	handler := dashboard.NewHandler(dashboard.Config{Live: noReading, RoundsDir: roundsDir})

	var replay struct {
		Verdict string `json:"verdict"`
	}

	if code := get(t, handler, "/api/replay/r0003", &replay); code != http.StatusOK || replay.Verdict != "pass" {
		t.Fatalf("replay of r0003: %d, %+v", code, replay)
	}

	for _, path := range []string{"/api/replay/r..0003", "/api/replay/r0004", "/api/replay/etc"} {
		if code := get(t, handler, path, nil); code != http.StatusNotFound {
			t.Fatalf("%s answered %d", path, code)
		}
	}
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
