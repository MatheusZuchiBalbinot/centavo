// The live panel feeds it the lines of a history still being written;
// `lab export-replay` feeds it a whole round.
package replay

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"centavo/lab/internal/checker"
	"centavo/lab/internal/history"
)

// maxDefenseEvents bounds the timeline a replay keeps. Defenses grow with the
// load, and a busy round would otherwise crowd out its own late attacks and
// its cure; the nemesis's events follow the scenario's schedule and are always
// kept. The counters see every event.
const maxDefenseEvents = 2_000

const (
	familyProcess   EventFamily = "process"
	familyFailpoint EventFamily = "failpoint"
	familyNetwork   EventFamily = "network"
	familyDatabase  EventFamily = "database"
	familyClock     EventFamily = "clock"
	familyPartner   EventFamily = "partner"
	familyHeal      EventFamily = "heal"
	familyDefense   EventFamily = "defense"
	familyState     EventFamily = "state"
)

const (
	networkStart = "start"
	networkEnd   = "end"
)

const (
	medianPercentile = 50
	tailPercentile   = 99
	percentScale     = 100
)

type Replay struct {
	Round           string          `json:"round"`
	Scenario        string          `json:"scenario"`
	Seed            string          `json:"seed"`
	Seconds         []Second        `json:"seconds"`
	Events          []Event         `json:"events"`
	Counters        Counters        `json:"counters"`
	Verdict         checker.Verdict `json:"verdict,omitempty"`
	Description     string          `json:"description,omitempty"`
	DurationSeconds int             `json:"duration_seconds,omitempty"`
	StartedAt       time.Time       `json:"started_at"`
	Defenses        string          `json:"defenses,omitempty"`
	Stopped         bool            `json:"stopped,omitempty"`
	Error           string          `json:"error,omitempty"`
	Checks          []Check         `json:"checks,omitempty"`
	BuiltAt         time.Time       `json:"built_at"`
}

type Second struct {
	T    int `json:"t"`
	OK   int `json:"ok"`
	Fail int `json:"fail"`
	Info int `json:"info"`
	// P50 and P99 are completion latencies in milliseconds.
	P50 float64 `json:"p50_ms"`
	P99 float64 `json:"p99_ms"`
}

type EventFamily string

// Kind, Target, Detail and Duration are the facts, for a client that words
// the event itself; Text is the same event already worded.
type Event struct {
	TMillis  int64            `json:"t_ms"`
	Family   EventFamily      `json:"family"`
	Kind     history.Function `json:"kind"`
	Target   string           `json:"target,omitempty"`
	Detail   string           `json:"detail,omitempty"`
	Duration string           `json:"duration,omitempty"`
	// Subject is what a defense protected (a lease, a topic) and Count how
	// many items one action covered.
	Subject string `json:"subject,omitempty"`
	Count   int    `json:"count,omitempty"`
	// Manual is an attack a person launched from the panel.
	Manual bool   `json:"manual,omitempty"`
	Text   string `json:"text"`
}

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

type Check struct {
	ID         string         `json:"id"`
	Status     checker.Status `json:"status"`
	Violations int            `json:"violations"`
}

type Builder struct {
	replay    Replay
	invokes   map[string]int64
	latencies map[int][]float64
	defenses  int
}

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
	State     string   `json:"state"`
	Defense   string   `json:"defense"`
	Detail    string   `json:"detail"`
	Count     int      `json:"count"`
	Via       string   `json:"via"`
}

// viaManual is the nemesis's word for an attack launched from the panel.
const viaManual = "manual"

// databaseTarget is the one database the lab attacks.
const databaseTarget = "postgres"

func NewBuilder(round, scenario, seed string) *Builder {
	replay := Replay{Round: round, Scenario: scenario, Seed: seed, Seconds: []Second{}, Events: []Event{}}

	return &Builder{replay: replay, invokes: map[string]int64{}, latencies: map[int][]float64{}}
}

// A line that is not a record is skipped, since the live file may end in a
// line still being written.
func (b *Builder) FeedLine(line []byte) {
	var record history.Record

	err := json.Unmarshal(line, &record)
	if err != nil {
		return
	}

	b.Feed(record)
}

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

func (b *Builder) Replay() Replay {
	replay := b.replay
	replay.Seconds = slices.Clone(b.replay.Seconds)
	replay.Events = slices.Clone(b.replay.Events)
	replay.BuiltAt = time.Now().UTC()

	for i := range replay.Seconds {
		latencies := slices.Clone(b.latencies[replay.Seconds[i].T])
		slices.Sort(latencies)
		replay.Seconds[i].P50 = percentile(latencies, medianPercentile)
		replay.Seconds[i].P99 = percentile(latencies, tailPercentile)
	}

	return replay
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

	isResolved := record.F == history.FunctionResolve && record.Type == history.TypeOK
	if isResolved {
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

func (b *Builder) secondAt(timeNS int64) *Second {
	t := int(timeNS / int64(time.Second))

	for len(b.replay.Seconds) <= t {
		b.replay.Seconds = append(b.replay.Seconds, Second{T: len(b.replay.Seconds)})
	}

	return &b.replay.Seconds[t]
}

// Nearest-rank method.
func percentile(sorted []float64, percent int) float64 {
	if len(sorted) == 0 {
		return 0
	}

	rank := (percent*len(sorted) + percentScale - 1) / percentScale

	return sorted[max(rank-1, 0)]
}

func (b *Builder) addEvent(record history.Record, event Event) {
	if event.Family == familyDefense {
		if b.defenses >= maxDefenseEvents {
			return
		}

		b.defenses++
	}

	event.TMillis = record.TimeNS / int64(time.Millisecond)
	event.Kind = record.F
	b.replay.Events = append(b.replay.Events, event)
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
		b.addEvent(record, Event{Family: familyProcess, Target: value.Target, Manual: value.Via == viaManual, Text: eventKilled + value.Target})
	case history.FunctionFailpointFired:
		counters.FailpointFires++
		text := fmt.Sprintf(eventFailpoint, value.Failpoint, value.Action, value.Instance)
		b.addEvent(record, Event{Family: familyFailpoint, Target: value.Instance, Detail: value.Failpoint, Text: text})
	case history.FunctionFreeze:
		counters.Freezes++
		text := fmt.Sprintf(eventFrozen, value.Target, value.Duration)
		b.addEvent(record, Event{Family: familyProcess, Target: value.Target, Duration: value.Duration, Manual: value.Via == viaManual, Text: text})
	case history.FunctionNetwork:
		b.feedNetwork(record, value)
	case history.FunctionDatabase:
		counters.Database++
		event := Event{
			Family: familyDatabase, Target: databaseTarget, Detail: value.Action, Duration: value.Duration,
			Manual: value.Via == viaManual, Text: eventDatabase + value.Action,
		}
		b.addEvent(record, event)
	case history.FunctionFailover:
		b.feedFailover(record, value)
	case history.FunctionSkewClock:
		counters.ClockSkews++
		text := fmt.Sprintf(eventClock, value.Targets, value.Offset)
		b.addEvent(record, Event{Family: familyClock, Target: strings.Join(value.Targets, ", "), Detail: value.Offset, Text: text})
	case history.FunctionArmPartner:
		counters.PartnerLies++
		b.addEvent(record, Event{Family: familyPartner, Detail: value.Fault, Text: eventPartnerLies + value.Fault})
	case history.FunctionHealStart:
		b.addEvent(record, Event{Family: familyHeal, Text: eventHealStarted})
	case history.FunctionHealComplete:
		b.addEvent(record, Event{Family: familyHeal, Text: eventHealCompleted})
	case history.FunctionProcessState:
		b.addEvent(record, Event{Family: familyState, Target: value.Target, Detail: value.State, Text: fmt.Sprintf(eventProcessState, value.Target, value.State)})
	case history.FunctionDefense:
		text := fmt.Sprintf(eventDefense, value.Target, value.Defense, value.Count)
		b.addEvent(record, Event{Family: familyDefense, Target: value.Target, Detail: value.Defense, Subject: value.Detail, Count: value.Count, Text: text})
	}
}

// Its end is marked with the subject "end", so the map knows when the link
// is back.
func (b *Builder) feedNetwork(record history.Record, value nemesisValue) {
	target := strings.Join(value.Proxies, ", ")

	if value.Phase == networkEnd {
		end := Event{
			Family: familyNetwork, Target: target, Detail: value.Toxic, Subject: networkEnd,
			Manual: value.Via == viaManual, Text: fmt.Sprintf(eventNetworkEnd, value.Proxies),
		}
		b.addEvent(record, end)

		return
	}

	if value.Phase != networkStart {
		return
	}

	b.replay.Counters.Network++
	text := fmt.Sprintf(eventNetwork, value.Toxic, value.Proxies)
	b.addEvent(record, Event{Family: familyNetwork, Target: target, Detail: value.Toxic, Manual: value.Via == viaManual, Text: text})
}

func (b *Builder) feedFailover(record history.Record, value nemesisValue) {
	if value.Phase != "promoted" {
		return
	}

	b.replay.Counters.Failovers++
	b.addEvent(record, Event{Family: familyDatabase, Text: eventFailover})
}
