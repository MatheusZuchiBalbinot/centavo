//go:build chaoslab

package failpoint

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"centavo/bank/internal/platform/correlation"
)

func TestHit_FiresAndRecordsTheEventBeforeActing(t *testing.T) {
	dir := armForTest(t, errorRule())
	ctx := correlation.With(context.Background(), "r0001-p01-000001")

	err := Hit(ctx, TransferAfterDebitBeforeCredit)
	if !errors.Is(err, ErrSynthetic) {
		t.Fatalf("got %v, want the synthetic error", err)
	}

	recorded := readEvents(t, filepath.Join(dir, "bank-api-test.jsonl"))
	isRecorded := len(recorded) == 1 && recorded[0].Failpoint == TransferAfterDebitBeforeCredit && recorded[0].CorrelationID == "r0001-p01-000001" && recorded[0].Fire == 1
	if !isRecorded {
		t.Fatalf("events: %+v", recorded)
	}
}

func TestHit_RespectsMatchSkipAndMaxFires(t *testing.T) {
	rule := errorRule()
	rule.Match = Match{CorrelationPrefix: "r0001-"}
	rule.SkipHits = 1
	rule.MaxFires = 2
	armForTest(t, rule)

	other := correlation.With(context.Background(), "r0002-p01-000001")
	if Hit(other, TransferAfterDebitBeforeCredit) != nil {
		t.Fatal("fired for a correlation outside the match")
	}

	mine := correlation.With(context.Background(), "r0001-p01-000001")

	var fired int

	for range 5 {
		if Hit(mine, TransferAfterDebitBeforeCredit) != nil {
			fired++
		}
	}

	if fired != 2 {
		t.Fatalf("fired %d times, want 2 (first hit skipped, at most 2 fires)", fired)
	}

	states := Rules(time.Now())
	isUnexpected := len(states) != 1 || states[0].Hits != 5 || states[0].Fires != 2
	if isUnexpected {
		t.Fatalf("rule state: %+v", states)
	}
}

func TestHit_SameSeedReplaysTheSameDecisions(t *testing.T) {
	rule := errorRule()
	rule.Probability = 0.3

	sequence := func() []bool {
		armForTest(t, rule)

		decisions := make([]bool, 50)
		for i := range decisions {
			decisions[i] = Hit(context.Background(), TransferAfterDebitBeforeCredit) != nil
		}

		return decisions
	}

	first, second := sequence(), sequence()

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("decision %d differs between two runs with the same seed", i)
		}
	}
}

func TestArm_RenewingKeepsCountersAndExpiryEndsTheRule(t *testing.T) {
	rule := errorRule()
	rule.MaxFires = 1
	armForTest(t, rule)

	_ = Hit(context.Background(), TransferAfterDebitBeforeCredit)

	err := Arm(TransferAfterDebitBeforeCredit, rule, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	if Hit(context.Background(), TransferAfterDebitBeforeCredit) != nil {
		t.Fatal("renewing the rule reset max_fires")
	}

	err = Arm(TransferAfterDebitBeforeCredit, errorRule(), time.Now().Add(-2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	if Hit(context.Background(), TransferAfterDebitBeforeCredit) != nil {
		t.Fatal("an expired rule fired")
	}
}

func TestHit_NeverFiresWithoutAPlaceToRecordIt(t *testing.T) {
	armForTest(t, errorRule())
	ConfigureEvents(EventsConfig{})

	if Hit(context.Background(), TransferAfterDebitBeforeCredit) != nil {
		t.Fatal("fired without recording the event")
	}
}

func TestArm_RejectsInvalidRules(t *testing.T) {
	cases := []struct {
		name string
		fp   Name
		rule Rule
	}{
		{name: "unknown failpoint", fp: "nowhere", rule: errorRule()},
		{name: "no ttl", fp: TransferAfterDebitBeforeCredit, rule: Rule{Action: ActionKill, Probability: 1}},
		{name: "ttl above an hour", fp: TransferAfterDebitBeforeCredit, rule: Rule{Action: ActionKill, Probability: 1, TTL: "2h"}},
		{name: "probability above one", fp: TransferAfterDebitBeforeCredit, rule: Rule{Action: ActionKill, Probability: 1.5, TTL: "1m"}},
		{name: "delay without duration", fp: TransferAfterDebitBeforeCredit, rule: Rule{Action: ActionDelay, Probability: 1, TTL: "1m"}},
		{name: "freeze without duration", fp: TransferAfterDebitBeforeCredit, rule: Rule{Action: ActionFreeze, Probability: 1, TTL: "1m"}},
		{name: "freeze longer than two minutes", fp: TransferAfterDebitBeforeCredit, rule: Rule{Action: ActionFreeze, Arg: "3m", Probability: 1, TTL: "1m"}},
	}

	for _, tc := range cases {
		err := Arm(tc.fp, tc.rule, time.Now())
		if err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

func TestArmFromSpec(t *testing.T) {
	DisarmAll()
	t.Cleanup(DisarmAll)

	err := ArmFromSpec("transfer.after_debit_before_credit=5%kill*50, api.after_commit_before_response=delay(2s)", 9, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	states := Rules(time.Now())
	if len(states) != 2 {
		t.Fatalf("rules: %+v", states)
	}

	first, second := states[0], states[1]
	isParsed := second.Rule.Action == ActionKill && second.Rule.Probability == 0.05 && second.Rule.MaxFires == 50 &&
		first.Rule.Action == ActionDelay && first.Rule.Arg == "2s" && first.Rule.Probability == 1
	if !isParsed {
		t.Fatalf("parsed rules: %+v", states)
	}

	err = ArmFromSpec("transfer.after_debit_before_credit=kill(", 0, time.Now())
	if !errors.Is(err, ErrInvalidRule) {
		t.Fatalf("malformed spec: got %v", err)
	}
}

func armForTest(t *testing.T, rule Rule) string {
	t.Helper()

	DisarmAll()
	t.Cleanup(DisarmAll)

	dir := t.TempDir()
	ConfigureEvents(EventsConfig{Dir: dir, Service: "bank-api", Instance: "bank-api-test"})
	t.Cleanup(func() {
		events.mu.Lock()
		defer events.mu.Unlock()

		if events.file != nil {
			_ = events.file.Close()
			events.file = nil
		}

		events.config = EventsConfig{}
	})

	err := Arm(TransferAfterDebitBeforeCredit, rule, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

func errorRule() Rule {
	return Rule{Action: ActionError, Arg: "conn_reset", Probability: 1, TTL: "1m", Seed: 7}
}

func readEvents(t *testing.T, path string) []event {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer file.Close()

	var recorded []event

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var e event

		err := json.Unmarshal(scanner.Bytes(), &e)
		if err != nil {
			t.Fatal(err)
		}

		recorded = append(recorded, e)
	}

	return recorded
}
