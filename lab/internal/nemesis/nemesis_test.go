package nemesis

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/internal/seed"
	"centavo/lab/scenarios"
)

func newTestNemesis(t *testing.T, eventsDir string) (*Nemesis, string) {
	t.Helper()

	historyPath := filepath.Join(t.TempDir(), "history.jsonl")

	writer, err := history.Create(historyPath, "r0042")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = writer.Close() })

	fault := scenarios.Fault{ID: "fp", Kind: scenarios.FaultFailpoint, Targets: []string{"bank-api"}, Failpoint: "transfer.after_debit_before_credit", Action: "kill", Probability: 0.1, MaxFires: 3}
	config := Config{
		Round: "r0042", Seed: seed.Seed(0x5eed), Scenario: scenarios.Scenario{Faults: []scenarios.Fault{fault}},
		History: writer, EventsDir: eventsDir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	return New(config), historyPath
}

func TestRuleFor_CountsDownTheFiresAndScopesTheRound(t *testing.T) {
	n, _ := newTestNemesis(t, t.TempDir())
	armed := *n.failpoints.faults[0]

	rule := n.failpoints.ruleFor(armed, "bank-api", 0)
	isFresh := rule.MaxFires == 3 && rule.Probability == 0.1 && rule.Match.CorrelationPrefix == "r0042-" && rule.TTL == ruleTTL
	if !isFresh {
		t.Fatalf("fresh rule: %+v", rule)
	}

	armed.fired = 2
	if rule := n.failpoints.ruleFor(armed, "bank-api", 0); rule.MaxFires != 1 {
		t.Fatalf("after two fires: %+v", rule)
	}

	armed.fired = 3
	if rule := n.failpoints.ruleFor(armed, "bank-api", 0); rule.Probability != 0 {
		t.Fatalf("exhausted rule still fires: %+v", rule)
	}

	sameIncarnation := n.failpoints.ruleFor(armed, "bank-api", 0).Seed
	nextIncarnation := n.failpoints.ruleFor(armed, "bank-api", 1).Seed

	isUnexpected := sameIncarnation != rule.Seed || sameIncarnation == nextIncarnation
	if isUnexpected {
		t.Fatal("the seed must repeat for the same incarnation and change after a restart")
	}
}

func TestCollectEvents_RecordsThisRoundAndCountsFires(t *testing.T) {
	eventsDir := t.TempDir()
	eventsPath := filepath.Join(eventsDir, "bank-api.jsonl")

	err := os.WriteFile(eventsPath, []byte(`{"instance":"bank-api","failpoint":"transfer.after_debit_before_credit","action":"kill","correlation_id":"r0041-p01-000001"}`+"\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	n, historyPath := newTestNemesis(t, eventsDir)

	lines := []string{
		`{"instance":"bank-api","failpoint":"transfer.after_debit_before_credit","action":"kill","correlation_id":"r0042-p01-000007"}`,
		`{"instance":"bank-api","failpoint":"transfer.after_debit_before_credit","action":"kill","correlation_id":"r0099-p01-000001"}`,
		`{"instance":"bank-api","failpoint":"api.after_commit_before_response","action":"kill","correlation_id":"r0042-p02-000003"}`,
	}
	appendTo(t, eventsPath, strings.Join(lines, "\n")+"\n"+`{"instance":"bank-api","fail`)

	n.failpoints.collectEvents()

	if fired := n.failpoints.faults[0].fired; fired != 1 {
		t.Fatalf("fault fired %d times, want 1 (the other round and the other failpoint do not count)", fired)
	}

	if events := n.Events(); len(events) != 2 {
		t.Fatalf("round events: %d, want 2", len(events))
	}

	appendTo(t, eventsPath, `point":"transfer.after_debit_before_credit","action":"kill","correlation_id":"r0042-p03-000001"}`+"\n")
	n.failpoints.collectEvents()

	if fired := n.failpoints.faults[0].fired; fired != 2 {
		t.Fatalf("the line finished later was not read: fired %d", fired)
	}

	err = n.config.History.Close()
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Count(string(content), `"f":"failpoint_fired"`); got != 3 {
		t.Fatalf("history has %d failpoint_fired records, want 3", got)
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

// TestClockFaults_AreRecordedAndCleared: the skew is in the history before
// it is set, and the faults of the scenario are the ones pushed.
func TestClockFaults_AreRecordedAndCleared(t *testing.T) {
	n, historyPath := newTestNemesis(t, t.TempDir())
	skew := scenarios.Fault{ID: "ahead", Kind: scenarios.FaultClock, Targets: []string{"bank-worker-2"}, Offset: 2 * time.Minute}
	n.config.Scenario.Faults = append(n.config.Scenario.Faults, skew)

	faults := n.clockFaults()

	isUnexpected := len(faults) != 1 || faults[0].ID != "ahead"
	if isUnexpected {
		t.Fatalf("clock faults: %+v", faults)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := n.skewClocks(ctx)
	if err != nil {
		t.Fatal(err)
	}

	n.wg.Wait()

	err = n.config.History.Close()
	if err != nil {
		t.Fatal(err)
	}

	content, err := os.ReadFile(historyPath)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), `"f":"skew_clock"`) || !strings.Contains(string(content), `"offset":"2m0s"`) {
		t.Fatalf("history without the skew: %s", content)
	}
}
