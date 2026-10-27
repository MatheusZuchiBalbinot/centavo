package nemesis

import (
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"centavo/lab/internal/docker"
	"centavo/lab/internal/history"
)

const watchInterval = time.Second

const defensesPattern = "*.defenses.ndjson"

const (
	processUp       = "up"
	processStarting = "starting"
	processFrozen   = "frozen"
	processDown     = "down"
)

type processState struct {
	Target string `json:"target"`
	State  string `json:"state"`
}

type defenseEvent struct {
	TS       time.Time `json:"ts"`
	Instance string    `json:"instance"`
	Defense  string    `json:"defense"`
	Detail   string    `json:"detail"`
	Count    int       `json:"count"`
}

type defenseValue struct {
	Defense string `json:"defense"`
	Target  string `json:"target"`
	Detail  string `json:"detail,omitempty"`
	Count   int    `json:"count"`
}

func (n *Nemesis) watch(ctx context.Context) {
	states := map[string]string{}
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	for {
		n.watchTargets(ctx, states)
		n.collectDefenses()

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// The first look records every target.
func (n *Nemesis) watchTargets(ctx context.Context, states map[string]string) {
	targets, err := n.config.Docker.Targets(ctx)
	if err != nil {
		return
	}

	for _, target := range targets {
		state := stateOf(target)
		if states[target.Service] == state {
			continue
		}

		states[target.Service] = state

		err := n.record(history.FunctionProcessState, processState{Target: target.Service, State: state})
		if err != nil {
			n.config.Logger.ErrorContext(ctx, "recording a process state", "target", target.Service, "error", err)
		}
	}
}

func stateOf(container docker.Container) string {
	switch {
	case container.State == docker.StatePaused:
		return processFrozen
	case container.IsHealthy():
		return processUp
	case container.State == docker.StateRunning:
		return processStarting
	default:
		return processDown
	}
}

// Only this round's: the stack runs one round at a time, so a defense after
// the round started belongs to it.
func (n *Nemesis) collectDefenses() {
	paths, err := filepath.Glob(filepath.Join(n.config.EventsDir, defensesPattern))
	if err != nil {
		return
	}

	for _, path := range paths {
		for _, line := range n.failpoints.readNewLines(path) {
			n.handleDefense(line)
		}
	}
}

func (n *Nemesis) handleDefense(line []byte) {
	var event defenseEvent

	err := json.Unmarshal(line, &event)
	if err != nil {
		return
	}

	if event.TS.Before(n.started) {
		return
	}

	value := defenseValue{Defense: event.Defense, Target: event.Instance, Detail: event.Detail, Count: event.Count}

	err = n.record(history.FunctionDefense, value)
	if err != nil {
		n.config.Logger.Error("recording a defense", "defense", event.Defense, "error", err)
	}
}
