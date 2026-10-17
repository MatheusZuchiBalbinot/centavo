// Package nemesis is the chaos bot (docs/chaos-bot.md): it arms failpoints,
// kills processes, records every injection in the history and cures
// everything at the end of a round.
package nemesis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"centavo/lab/internal/docker"
	"centavo/lab/internal/history"
	"centavo/lab/internal/seed"
	"centavo/lab/scenarios"
)

const (
	chaosTokenHeader = "X-Chaos-Token" //nolint:gosec // G101: a header name, not a secret.
	healthPath       = "/internal/health"
	healPollInterval = time.Second
	requestTimeout   = 3 * time.Second
	expectedEnv      = "chaoslab"
	maxResponseBytes = 1 << 20
)

var (
	ErrUnsafeTarget = errors.New("a target is not a chaoslab build in the chaoslab environment")
	ErrHealTimeout  = errors.New("targets did not become healthy before the heal timeout")
)

// target is one process the bot may hit.
type target struct {
	service  string
	binary   string
	adminURL string
	// chaosURL is empty for processes without failpoints.
	chaosURL string
}

// targets are the chaos targets of the Compose stack (docs/architecture.md,
// section 2); their ports never leave the Compose network.
var targets = []target{
	{service: "bank-api", binary: "/app/bank", adminURL: "http://bank-api:9090", chaosURL: "http://bank-api:9091"},
	{service: "bank-worker-1", binary: "/app/bank", adminURL: "http://bank-worker-1:9090", chaosURL: "http://bank-worker-1:9091"},
	{service: "bank-worker-2", binary: "/app/bank", adminURL: "http://bank-worker-2:9090", chaosURL: "http://bank-worker-2:9091"},
	{service: "partner", binary: "/app/partner", adminURL: "http://partner:9092"},
}

// Config is one round of chaos.
type Config struct {
	Round      string
	Seed       seed.Seed
	Scenario   scenarios.Scenario
	History    *history.Writer
	Docker     docker.Client
	ChaosToken string
	EventsDir  string
	Logger     *slog.Logger
	// PartnerAdminURL is the partner's admin port, where its lies are set.
	PartnerAdminURL string
	ParticipantID   string
}

// Nemesis runs the faults of one round.
type Nemesis struct {
	config  Config
	http    *http.Client
	started time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
	// freezes are the container pauses in progress; the cure waits for them.
	freezes sync.WaitGroup

	failpoints *failpointController
	// toxiproxyURL is Toxiproxy's API; tests point it at a fake.
	toxiproxyURL string
}

// New builds the nemesis of a round.
func New(config Config) *Nemesis {
	n := &Nemesis{config: config, http: &http.Client{Timeout: requestTimeout}, started: time.Now().UTC(), toxiproxyURL: toxiproxyURL}
	n.failpoints = newFailpointController(n)

	return n
}

// Preflight is lock 2 seen from the lab: it refuses to run against anything
// that is not a chaoslab build in the chaoslab environment, then cures what
// a previous run may have left behind (toxics never expire by themselves).
func (n *Nemesis) Preflight(ctx context.Context) error {
	err := n.waitHealthy(ctx, n.config.Scenario.HealTimeout)
	if err != nil {
		return err
	}

	for _, t := range targets {
		err := n.checkSafety(ctx, t)
		if err != nil {
			return err
		}
	}

	n.clearFailpoints(ctx)
	n.clearPartner(ctx)
	n.clearClocks(ctx)
	n.resetNetwork(ctx)

	return nil
}

// Start arms the scenario's faults; they run until Heal.
func (n *Nemesis) Start(ctx context.Context) error {
	ctx, n.cancel = context.WithCancel(ctx)

	err := n.failpoints.arm(ctx)
	if err != nil {
		return err
	}

	n.wg.Go(func() { n.failpoints.run(ctx) })

	err = n.armPartner(ctx)
	if err != nil {
		return err
	}

	n.wg.Go(func() { n.renewPartner(ctx) })

	err = n.skewClocks(ctx)
	if err != nil {
		return err
	}

	n.startNetwork(ctx)

	for _, fault := range n.config.Scenario.Faults {
		if fault.Kind == scenarios.FaultProcess {
			n.wg.Go(func() { n.killRandomly(ctx, fault) })
		}
	}

	return nil
}

// Heal is the cure of docs/chaos-bot.md, section 3.1: stop injecting, clear
// every failpoint, resume and start every target, and wait until all of them
// are healthy. heal_complete marks t_h.
func (n *Nemesis) Heal(ctx context.Context) error {
	if n.cancel != nil {
		n.cancel()
	}

	n.wg.Wait()
	n.freezes.Wait()

	err := n.record(history.FunctionHealStart, nil)
	if err != nil {
		return err
	}

	n.clearFailpoints(ctx)
	n.clearPartner(ctx)
	n.clearClocks(ctx)
	n.resetNetwork(ctx)

	err = n.waitHealthy(ctx, n.config.Scenario.HealTimeout)
	if err != nil {
		return err
	}

	// Last look at the events: a failpoint may have fired right before the cure.
	n.failpoints.collectEvents()

	return n.record(history.FunctionHealComplete, nil)
}

// Events returns the failpoint events of the round, for events.jsonl.
func (n *Nemesis) Events() []json.RawMessage {
	return n.failpoints.roundEvents()
}

// waitHealthy resumes and starts every target until all are healthy.
func (n *Nemesis) waitHealthy(ctx context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		isEveryoneHealthy, err := n.nudgeTargets(ctx)
		if err != nil {
			return err
		}

		if isEveryoneHealthy {
			return nil
		}

		select {
		case <-ctx.Done():
			return ErrHealTimeout
		case <-time.After(healPollInterval):
		}
	}
}

// nudgeTargets unpauses paused targets and starts stopped ones.
func (n *Nemesis) nudgeTargets(ctx context.Context) (bool, error) {
	isEveryoneHealthy := true

	for _, t := range targets {
		container, err := n.config.Docker.Find(ctx, t.service)
		if err != nil {
			return false, err
		}

		if container.IsPaused {
			err = n.config.Docker.Unpause(ctx, container.ID)
		}

		if !container.IsRunning && err == nil {
			err = n.config.Docker.Start(ctx, container.ID)
		}

		if err != nil {
			n.config.Logger.WarnContext(ctx, "nudging a target", "target", t.service, "error", err)
		}

		isEveryoneHealthy = isEveryoneHealthy && container.IsHealthy()
	}

	return isEveryoneHealthy, nil
}

type healthBody struct {
	ChaosBuild bool   `json:"chaos_build"`
	Env        string `json:"env"`
}

func (n *Nemesis) checkSafety(ctx context.Context, t target) error {
	var health healthBody

	err := n.getJSON(ctx, t.adminURL+healthPath, &health)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnsafeTarget, t.service, err)
	}

	isSafe := health.ChaosBuild && health.Env == expectedEnv
	if !isSafe {
		return fmt.Errorf("%w: %s reports chaos_build=%t env=%q", ErrUnsafeTarget, t.service, health.ChaosBuild, health.Env)
	}

	return nil
}

// clearFailpoints asks every bank process to drop its rules; a process that
// is down has none to drop.
func (n *Nemesis) clearFailpoints(ctx context.Context) {
	for _, t := range targets {
		if t.chaosURL == "" {
			continue
		}

		err := n.chaosCall(ctx, http.MethodDelete, t.chaosURL+"/chaos/failpoints", nil)
		if err != nil {
			n.config.Logger.WarnContext(ctx, "clearing failpoints", "target", t.service, "error", err)
		}
	}
}

// record writes a nemesis record durably, before the action it announces.
func (n *Nemesis) record(function history.Function, value any) error {
	record := history.Record{Process: history.Nemesis(), Type: history.TypeInfo, F: function}

	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}

		record.Value = encoded
	}

	_, err := n.config.History.AppendDurable(record)

	return err
}

func (n *Nemesis) chaosCall(ctx context.Context, method, url string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, method, url, bytesReader(body))
	if err != nil {
		return err
	}

	request.Header.Set(chaosTokenHeader, n.config.ChaosToken)
	request.Header.Set("Content-Type", "application/json")

	response, err := n.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	// The body only enriches the error message; failing to read it changes nothing.
	content, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%s %s answered %d: %s", method, url, response.StatusCode, content)
	}

	return nil
}

func (n *Nemesis) getJSON(ctx context.Context, url string, into any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	response, err := n.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	return json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(into)
}

func targetByService(service string) (target, bool) {
	for _, t := range targets {
		if t.service == service {
			return t, true
		}
	}

	return target{}, false
}
