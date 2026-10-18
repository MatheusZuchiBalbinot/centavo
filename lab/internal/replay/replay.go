// Package replay turns a round's history into what the panel draws
// (docs/architecture.md, section 13): operations and latency per second,
// the nemesis timeline and the chaos counters. The live panel feeds it the
// lines of a history still being written; `lab export-replay` feeds it a
// whole round, for the replay page.
package replay

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"centavo/lab/internal/history"
)

// maxEvents bounds the timeline a replay keeps; the counters see them all.
const maxEvents = 2_000

// Replay is one round as the panel plays it.
type Replay struct {
	Round    string    `json:"round"`
	Scenario string    `json:"scenario"`
	Seed     string    `json:"seed"`
	Seconds  []Second  `json:"seconds"`
	Events   []Event   `json:"events"`
	Counters Counters  `json:"counters"`
	Verdict  string    `json:"verdict,omitempty"`
	Checks   []Check   `json:"checks,omitempty"`
	BuiltAt  time.Time `json:"built_at"`
}

// Second is what the clients saw in one second of the round.
type Second struct {
	T    int `json:"t"`
	OK   int `json:"ok"`
	Fail int `json:"fail"`
	Info int `json:"info"`
	// P50 and P99 are completion latencies in milliseconds.
	P50 float64 `json:"p50_ms"`
	P99 float64 `json:"p99_ms"`
}

// Event is one entry of the chaos timeline.
type Event struct {
	TMillis int64  `json:"t_ms"`
	Family  string `json:"family"`
	Text    string `json:"text"`
}

// Counters add up the chaos of the round.
type Counters struct {
	Kills          int `json:"kills"`
	FailpointFires int `json:"failpoint_fires"`
	Network        int `json:"network_episodes"`
	Database       int `json:"database_faults"`
	Freezes        int `json:"freezes"`
	Failovers      int `json:"failovers"`
	ClockSkews     int `json:"clock_skews"`
	PartnerLies    int `json:"partner_lies"`
	Resolved       int `json:"resolved"`
}

// Check is one invariant's result, from the report.
type Check struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Violations int    `json:"violations"`
}

// Builder accumulates a replay a record at a time.
type Builder struct {
	replay    Replay
	invokes   map[string]int64
	latencies map[int][]float64
}

// NewBuilder starts the replay of a round.
func NewBuilder(round, scenario, seed string) *Builder {
	replay := Replay{Round: round, Scenario: scenario, Seed: seed, Seconds: []Second{}, Events: []Event{}}

	return &Builder{replay: replay, invokes: map[string]int64{}, latencies: map[int][]float64{}}
}

// FeedLine takes one line of history.jsonl; a line that is not a record is
// skipped, since the live file may end in a line still being written.
func (b *Builder) FeedLine(line []byte) {
	var record history.Record

	err := json.Unmarshal(line, &record)
	if err != nil {
		return
	}

	b.Feed(record)
}

// Feed takes one record.
func (b *Builder) Feed(record history.Record) {
	if record.Process.IsNemesis() {
		b.feedNemesis(record)

		return
	}

	if record.Type == history.TypeInvoke {
		b.invokes[record.Op] = record.TimeNS

		return
	}

	if !record.IsCompletion() {
		return
	}

	b.feedCompletion(record)
}

func (b *Builder) feedCompletion(record history.Record) {
	second := b.secondAt(record.TimeNS)

	// feed passes only completions.
	//exhaustive:ignore
	switch record.Type {
	case history.TypeOK:
		second.OK++
	case history.TypeFail:
		second.Fail++
	default:
		second.Info++
	}

	if record.F == history.FunctionResolve && record.Type == history.TypeOK {
		b.replay.Counters.Resolved++
	}

	invokedAt, isKnown := b.invokes[record.Op]
	if !isKnown {
		return
	}

	delete(b.invokes, record.Op)

	latency := float64(record.TimeNS-invokedAt) / float64(time.Millisecond)
	b.latencies[second.T] = append(b.latencies[second.T], latency)
}

// secondAt returns the second a time falls in, growing the series as needed.
func (b *Builder) secondAt(timeNS int64) *Second {
	t := int(timeNS / int64(time.Second))

	for len(b.replay.Seconds) <= t {
		b.replay.Seconds = append(b.replay.Seconds, Second{T: len(b.replay.Seconds)})
	}

	return &b.replay.Seconds[t]
}

// Replay returns the replay so far, with the latency percentiles computed.
func (b *Builder) Replay() Replay {
	replay := b.replay
	replay.Seconds = slices.Clone(b.replay.Seconds)
	replay.Events = slices.Clone(b.replay.Events)
	replay.BuiltAt = time.Now().UTC()

	for i := range replay.Seconds {
		latencies := slices.Clone(b.latencies[replay.Seconds[i].T])
		slices.Sort(latencies)
		replay.Seconds[i].P50 = percentile(latencies, 50)
		replay.Seconds[i].P99 = percentile(latencies, 99)
	}

	return replay
}

// percentile reads a sorted slice by the nearest-rank method.
func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}

	rank := (p*len(sorted) + 99) / 100

	return sorted[max(rank-1, 0)]
}

func (b *Builder) addEvent(record history.Record, family, text string) {
	if len(b.replay.Events) >= maxEvents {
		return
	}

	b.replay.Events = append(b.replay.Events, Event{TMillis: record.TimeNS / int64(time.Millisecond), Family: family, Text: text})
}

// nemesisValue reads the fields the timeline shows, whatever the record.
type nemesisValue struct {
	Target    string   `json:"target"`
	Instance  string   `json:"instance"`
	Failpoint string   `json:"failpoint"`
	Action    string   `json:"action"`
	Fault     string   `json:"fault"`
	Proxies   []string `json:"proxies"`
	Toxic     string   `json:"toxic"`
	Phase     string   `json:"phase"`
	Offset    string   `json:"offset"`
	Duration  string   `json:"duration"`
	Targets   []string `json:"targets"`
}

func (b *Builder) feedNemesis(record history.Record) {
	var value nemesisValue

	// A value the timeline cannot read still counts as an event.
	_ = json.Unmarshal(record.Value, &value)

	counters := &b.replay.Counters

	// Client operations are counted per second, not told as events.
	//exhaustive:ignore
	switch record.F {
	case history.FunctionKill:
		counters.Kills++
		b.addEvent(record, "process", "processo morto: "+value.Target)
	case history.FunctionFailpointFired:
		counters.FailpointFires++
		b.addEvent(record, "failpoint", fmt.Sprintf("failpoint %s (%s) em %s", value.Failpoint, value.Action, value.Instance))
	case history.FunctionFreeze:
		counters.Freezes++
		b.addEvent(record, "process", fmt.Sprintf("contêiner congelado: %s por %s", value.Target, value.Duration))
	case history.FunctionNetwork:
		b.feedNetwork(record, value)
	case history.FunctionDatabase:
		counters.Database++
		b.addEvent(record, "database", "banco de dados: "+value.Action)
	case history.FunctionFailover:
		b.feedFailover(record, value)
	case history.FunctionSkewClock:
		counters.ClockSkews++
		b.addEvent(record, "clock", fmt.Sprintf("relógio desviado em %s: %s", value.Targets, value.Offset))
	case history.FunctionArmPartner:
		counters.PartnerLies++
		b.addEvent(record, "partner", "o parceiro passa a mentir: "+value.Fault)
	case history.FunctionHealStart:
		b.addEvent(record, "heal", "cura começou")
	case history.FunctionHealComplete:
		b.addEvent(record, "heal", "cura terminou")
	}
}

func (b *Builder) feedNetwork(record history.Record, value nemesisValue) {
	if value.Phase != "start" {
		return
	}

	b.replay.Counters.Network++
	b.addEvent(record, "network", fmt.Sprintf("rede: %s em %v", value.Toxic, value.Proxies))
}

func (b *Builder) feedFailover(record history.Record, value nemesisValue) {
	if value.Phase != "promoted" {
		return
	}

	b.replay.Counters.Failovers++
	b.addEvent(record, "database", "failover: réplica promovida")
}
