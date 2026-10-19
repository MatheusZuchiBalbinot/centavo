package nemesis

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"centavo/lab/internal/docker"
)

func TestStateOf_NamesWhatTheMapShows(t *testing.T) {
	testCases := []struct {
		container docker.Container
		state     string
	}{
		{docker.Container{State: docker.StatePaused}, processFrozen},
		{docker.Container{State: docker.StateRunning, Health: docker.HealthHealthy}, processUp},
		{docker.Container{State: docker.StateRunning, Health: "starting"}, processStarting},
		{docker.Container{State: docker.StateStopped}, processDown},
	}

	for _, tc := range testCases {
		if got := stateOf(tc.container); got != tc.state {
			t.Fatalf("%+v: %s, want %s", tc.container, got, tc.state)
		}
	}
}

func TestCollectDefenses_RecordsOnlyThisRoundsDefensesOnce(t *testing.T) {
	eventsDir := t.TempDir()
	bot, historyPath := newTestNemesis(t, eventsDir)

	before := bot.started.Add(-time.Minute).Format(time.RFC3339Nano)
	after := bot.started.Add(time.Second).Format(time.RFC3339Nano)
	lines := `{"ts":"` + before + `","instance":"bank-worker-1","defense":"outbox.resumed","count":2}` + "\n" +
		`{"ts":"` + after + `","instance":"bank-worker-2","defense":"lease.acquired","detail":"reconciler","count":1}` + "\n"

	err := os.WriteFile(filepath.Join(eventsDir, "bank-worker-2.defenses.ndjson"), []byte(lines), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	bot.collectDefenses()
	bot.collectDefenses()

	content, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}

	recorded := strings.Count(string(content), `"f":"defense"`)
	isRecorded := recorded == 1 && strings.Contains(string(content), `"defense":"lease.acquired"`) && strings.Contains(string(content), `"target":"bank-worker-2"`)

	if !isRecorded {
		t.Fatalf("history: %s", content)
	}
}
