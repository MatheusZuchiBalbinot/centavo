package replay

import (
	"encoding/json"
	"testing"
	"time"

	"centavo/lab/internal/history"
)

func at(d time.Duration) int64 { return int64(d) }

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

	isChaos := replay.Counters.Kills == 1 && len(replay.Events) == 1 && replay.Events[0].TMillis == 2000 && replay.Events[0].Family == "process"
	if !isChaos {
		t.Fatalf("counters %+v, events %+v", replay.Counters, replay.Events)
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
