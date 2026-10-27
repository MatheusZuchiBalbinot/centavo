package replay

import (
	"encoding/json"
	"testing"
	"time"

	"centavo/lab/internal/history"
)

func TestBuilder_CountsSecondsLatencyAndChaos(t *testing.T) {
	t.Parallel()

	b := NewBuilder("r0007", "smoke", "0x5eed")
	client := history.Client(1)

	b.Feed(history.Record{Process: client, Type: history.TypeInvoke, F: history.FunctionDeposit, Op: "op-1", TimeNS: at(100 * time.Millisecond)})
	b.Feed(history.Record{Process: client, Type: history.TypeOK, F: history.FunctionDeposit, Op: "op-1", TimeNS: at(300 * time.Millisecond)})
	b.Feed(history.Record{Process: client, Type: history.TypeInvoke, F: history.FunctionTransfer, Op: "op-2", TimeNS: at(1500 * time.Millisecond)})
	b.Feed(history.Record{Process: client, Type: history.TypeInfo, F: history.FunctionTransfer, Op: "op-2", TimeNS: at(2500 * time.Millisecond)})

	kill, err := json.Marshal(map[string]string{"target": "bank-worker-1"})
	if err != nil {
		t.Fatal(err)
	}

	b.Feed(history.Record{Process: history.Nemesis(), Type: history.TypeInfo, F: history.FunctionKill, Value: kill, TimeNS: at(2 * time.Second)})
	b.FeedLine([]byte(`{"index": 9, "proc`))

	replay := b.Replay()

	isSeries := len(replay.Seconds) == 3 && replay.Seconds[0].OK == 1 && replay.Seconds[0].P50 == 200 && replay.Seconds[2].Info == 1 && replay.Seconds[2].P99 == 1000
	if !isSeries {
		t.Fatalf("seconds: %+v", replay.Seconds)
	}

	isChaos := replay.Counters.Kills == 1 && len(replay.Events) == 1 && replay.Events[0].TMillis == 2000 && replay.Events[0].Family == "process" &&
		replay.Events[0].Kind == history.FunctionKill && replay.Events[0].Target == "bank-worker-1"
	if !isChaos {
		t.Fatalf("counters %+v, events %+v", replay.Counters, replay.Events)
	}
}

func TestBuilder_TellsDefensesAndProcessStatesWithTheirFacts(t *testing.T) {
	t.Parallel()

	b := NewBuilder("r0001", "kill-after-commit", "0x5eed")
	nemesis := history.Nemesis()

	state := []byte(`{"target":"bank-worker-1","state":"down"}`)
	defense := []byte(`{"defense":"lease.acquired","target":"bank-worker-2","detail":"reconciler","count":1}`)

	b.Feed(history.Record{Process: nemesis, Type: history.TypeInfo, F: history.FunctionProcessState, Value: state, TimeNS: int64(time.Second)})
	b.Feed(history.Record{Process: nemesis, Type: history.TypeInfo, F: history.FunctionDefense, Value: defense, TimeNS: int64(3 * time.Second)})

	events := b.Replay().Events
	if len(events) != 2 {
		t.Fatalf("events: %+v", events)
	}

	down, taken := events[0], events[1]

	isState := down.Kind == history.FunctionProcessState && down.Target == "bank-worker-1" && down.Detail == "down"
	isDefense := taken.Kind == history.FunctionDefense && taken.Target == "bank-worker-2" && taken.Detail == "lease.acquired" && taken.Subject == "reconciler" && taken.Count == 1

	if !isState || !isDefense {
		t.Fatalf("state %+v, defense %+v", down, taken)
	}
}

func TestPercentile_NearestRank(t *testing.T) {
	t.Parallel()

	sorted := []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100}

	if got := percentile(sorted, 50); got != 50 {
		t.Fatalf("p50 = %v", got)
	}

	if got := percentile(sorted, 99); got != 100 {
		t.Fatalf("p99 = %v", got)
	}

	if got := percentile(nil, 50); got != 0 {
		t.Fatalf("p50 of nothing = %v", got)
	}
}

func at(d time.Duration) int64 { return int64(d) }

func TestBuilder_MarksTheAttacksAPersonLaunched(t *testing.T) {
	t.Parallel()

	b := NewBuilder("r0001", "random-kills", "0x5eed")
	nemesis := history.Nemesis()

	byHand := []byte(`{"target":"bank-worker-1","via":"manual"}`)
	random := []byte(`{"target":"bank-api","via":"random","fault":"kills"}`)
	database := []byte(`{"fault":"manual-x","action":"kill_postmaster","via":"manual"}`)

	b.Feed(history.Record{Process: nemesis, Type: history.TypeInfo, F: history.FunctionKill, Value: byHand, TimeNS: int64(time.Second)})
	b.Feed(history.Record{Process: nemesis, Type: history.TypeInfo, F: history.FunctionKill, Value: random, TimeNS: int64(2 * time.Second)})
	b.Feed(history.Record{Process: nemesis, Type: history.TypeInfo, F: history.FunctionDatabase, Value: database, TimeNS: int64(3 * time.Second)})

	events := b.Replay().Events
	if len(events) != 3 {
		t.Fatalf("events: %+v", events)
	}

	isRight := events[0].Manual && !events[1].Manual && events[2].Manual && events[2].Target == databaseTarget
	if !isRight {
		t.Fatalf("events: %+v", events)
	}
}
