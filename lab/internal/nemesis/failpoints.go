package nemesis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/scenarios"
)

const (
	// renewInterval and ruleTTL make every rule a lease: if the lab dies, the
	// rules expire within ruleTTL (lock 6). A restarted process gets its
	// rules back within renewInterval (docs/chaos-bot.md, section 5.4).
	renewInterval = 2 * time.Second
	ruleTTL       = "30s"
	eventsPattern = "*.jsonl"
)

// failpointRule is the rule the bank's chaos port accepts. The lab keeps its
// own copy of the contract: it never imports the bank.
type failpointRule struct {
	Action      string         `json:"action"`
	Arg         string         `json:"arg,omitempty"`
	Probability float64        `json:"probability"`
	MaxFires    int            `json:"max_fires,omitempty"`
	Match       failpointMatch `json:"match"`
	TTL         string         `json:"ttl"`
	Seed        uint64         `json:"seed"`
}

type failpointMatch struct {
	CorrelationPrefix string `json:"correlation_prefix"`
}

// failpointEvent is what a service writes when a failpoint fires.
type failpointEvent struct {
	Instance      string `json:"instance"`
	Failpoint     string `json:"failpoint"`
	Action        string `json:"action"`
	Arg           string `json:"arg,omitempty"`
	CorrelationID string `json:"correlation_id"`
}

// actionFreeze is the failpoint action the lab completes with docker pause.
const actionFreeze = "freeze"

type armedFault struct {
	fault scenarios.Fault
	fired int
}

type failpointController struct {
	n *Nemesis

	mu      sync.Mutex
	faults  []*armedFault
	offsets map[string]int64
	events  []json.RawMessage
}

func newFailpointController(n *Nemesis) *failpointController {
	c := &failpointController{n: n, offsets: map[string]int64{}}

	for _, fault := range n.config.Scenario.Faults {
		if fault.Kind == scenarios.FaultFailpoint {
			c.faults = append(c.faults, &armedFault{fault: fault})
		}
	}

	// Events written before this round belong to other rounds.
	for _, path := range c.eventFiles() {
		info, err := os.Stat(path)
		if err == nil {
			c.offsets[path] = info.Size()
		}
	}

	return c
}

type armRecord struct {
	Fault       string   `json:"fault"`
	Failpoint   string   `json:"failpoint"`
	Targets     []string `json:"targets"`
	Action      string   `json:"action"`
	Probability float64  `json:"probability"`
	MaxFires    int      `json:"max_fires"`
}

func (c *failpointController) arm(ctx context.Context) error {
	for _, armed := range c.faults {
		fault := armed.fault
		value := armRecord{
			Fault:       fault.ID,
			Failpoint:   fault.Failpoint,
			Targets:     fault.Targets,
			Action:      fault.Action,
			Probability: fault.Probability,
			MaxFires:    fault.MaxFires,
		}

		err := c.n.record(history.FunctionArmFailpoint, value)
		if err != nil {
			return err
		}
	}

	c.push(ctx)

	return nil
}

func (c *failpointController) run(ctx context.Context) {
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		c.collectEvents()
		c.push(ctx)
	}
}

// push (re)arms every failpoint fault on every target. The rule carries the
// fires left: when it changes the bank replaces the rule, and when none are
// left the probability drops to zero.
func (c *failpointController) push(ctx context.Context) {
	for _, armed := range c.snapshot() {
		for _, service := range armed.fault.Targets {
			c.pushTo(ctx, armed, service)
		}
	}
}

func (c *failpointController) pushTo(ctx context.Context, armed armedFault, service string) {
	t, isKnown := targetByService(service)
	if !isKnown || t.chaosURL == "" {
		return
	}

	container, err := c.n.config.Docker.Find(ctx, service)
	if err != nil {
		return
	}

	rule := c.ruleFor(armed, service, container.RestartCount)

	body, err := json.Marshal(rule)
	if err != nil {
		return
	}

	err = c.n.chaosCall(ctx, http.MethodPut, t.chaosURL+"/chaos/failpoints/"+armed.fault.Failpoint, body)
	if err != nil {
		// A target that is down gets the rule on the next renewal.
		c.n.config.Logger.DebugContext(ctx, "arming a failpoint", "target", service, "error", err)
	}
}

// ruleFor derives the rule's seed from the round seed, the target and its
// restart count: each incarnation of a process gets its own decisions, and
// the same round replays them (docs/chaos-bot.md, section 5.5).
func (c *failpointController) ruleFor(armed armedFault, service string, restartCount int) failpointRule {
	fault := armed.fault
	stream := fmt.Sprintf("failpoints.%s#%d/%s", service, restartCount, fault.ID)

	rule := failpointRule{
		Action:      fault.Action,
		Arg:         fault.Arg,
		Probability: fault.Probability,
		Match:       failpointMatch{CorrelationPrefix: c.n.config.Round + "-"},
		TTL:         ruleTTL,
		Seed:        c.n.config.Seed.Stream(stream).Uint64(),
	}

	if fault.MaxFires == 0 {
		return rule
	}

	remaining := fault.MaxFires - armed.fired
	if remaining <= 0 {
		rule.Probability = 0

		return rule
	}

	rule.MaxFires = remaining

	return rule
}

func (c *failpointController) snapshot() []armedFault {
	c.mu.Lock()
	defer c.mu.Unlock()

	copies := make([]armedFault, 0, len(c.faults))
	for _, armed := range c.faults {
		copies = append(copies, *armed)
	}

	return copies
}

// collectEvents reads what the services wrote since the last look and puts
// every fired failpoint of this round in the history.
func (c *failpointController) collectEvents() {
	for _, path := range c.eventFiles() {
		for _, line := range c.readNewLines(path) {
			c.handleEvent(line)
		}
	}
}

func (c *failpointController) handleEvent(line []byte) {
	var event failpointEvent

	err := json.Unmarshal(line, &event)
	if err != nil {
		return
	}

	isThisRound := strings.HasPrefix(event.CorrelationID, c.n.config.Round+"-")
	if !isThisRound {
		return
	}

	c.mu.Lock()
	c.events = append(c.events, slices.Clone(line))

	for _, armed := range c.faults {
		isThisFault := armed.fault.Failpoint == event.Failpoint && slices.Contains(armed.fault.Targets, event.Instance)
		if isThisFault {
			armed.fired++
		}
	}
	c.mu.Unlock()

	err = c.n.record(history.FunctionFailpointFired, event)
	if err != nil {
		c.n.config.Logger.Error("recording a fired failpoint", "error", err)
	}

	if event.Action == actionFreeze {
		c.n.freeze(event)
	}
}

// readNewLines returns the complete lines appended to path since the last
// read; a line still being written waits for the next look.
func (c *failpointController) readNewLines(path string) [][]byte {
	file, err := os.Open(path) //nolint:gosec // G304: files of the configured events directory.
	if err != nil {
		return nil
	}
	defer file.Close()

	c.mu.Lock()
	offset := c.offsets[path]
	c.mu.Unlock()

	_, err = file.Seek(offset, io.SeekStart)
	if err != nil {
		return nil
	}

	content, err := io.ReadAll(file)
	if err != nil {
		return nil
	}

	complete := bytes.LastIndexByte(content, '\n') + 1

	c.mu.Lock()
	c.offsets[path] = offset + int64(complete)
	c.mu.Unlock()

	var lines [][]byte

	for line := range bytes.SplitSeq(content[:complete], []byte{'\n'}) {
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}

	return lines
}

func (c *failpointController) eventFiles() []string {
	paths, err := filepath.Glob(filepath.Join(c.n.config.EventsDir, eventsPattern))
	if err != nil {
		return nil
	}

	return paths
}

func (c *failpointController) roundEvents() []json.RawMessage {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.events)
}

func bytesReader(body []byte) io.Reader {
	if body == nil {
		return nil
	}

	return bytes.NewReader(body)
}
