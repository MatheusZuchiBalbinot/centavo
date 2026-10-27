// Package runner starts the rounds the panel asks for, one at a time. The
// round itself runs in its own process, as `lab run` does from a terminal.
package runner

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"centavo/lab/internal/attacks"
)

type Status string

const (
	StatusIdle     Status = "idle"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
)

var (
	ErrBusy = errors.New("a round is already running")
	ErrIdle = errors.New("no round is running")
)

type Request struct {
	Attack   attacks.ID
	Scenario string
	Duration time.Duration
}

type State struct {
	Status    Status
	Request   Request
	StartedAt time.Time
}

type Process interface {
	// The cure and the check still run.
	StopAttack() error
	Wait() error
}

// Returns at once; ErrBusy when a round started elsewhere already holds the
// lab.
type LaunchFunc func(ctx context.Context, request Request) (Process, error)

type Runner struct {
	ctx    context.Context
	launch LaunchFunc
	logger *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	state   State
	process Process
	done    chan struct{}
}

func New(ctx context.Context, launch LaunchFunc, logger *slog.Logger) *Runner {
	return &Runner{ctx: ctx, launch: launch, logger: logger, now: time.Now, state: State{Status: StatusIdle}}
}

func (r *Runner) Start(request Request) (State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Status != StatusIdle {
		return r.state, ErrBusy
	}

	process, err := r.launch(r.ctx, request)
	if err != nil {
		return r.state, err
	}

	r.state = State{Status: StatusRunning, Request: request, StartedAt: r.now().UTC()}
	r.process = process
	r.done = make(chan struct{})

	go r.wait(process, r.done)

	return r.state, nil
}

// Asking again while it stops is a no-op, so a double click is harmless.
func (r *Runner) Stop() (State, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch r.state.Status {
	case StatusIdle:
		return r.state, ErrIdle
	case StatusStopping:
		return r.state, nil
	case StatusRunning:
	}

	err := r.process.StopAttack()
	if err != nil {
		return r.state, err
	}

	r.state.Status = StatusStopping

	return r.state, nil
}

func (r *Runner) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.state
}

func (r *Runner) Done() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.done
}

func (r *Runner) wait(process Process, done chan struct{}) {
	err := process.Wait()

	r.mu.Lock()
	request := r.state.Request
	r.state = State{Status: StatusIdle}
	r.process = nil
	r.done = nil
	r.mu.Unlock()

	close(done)

	// A failed or broken round is still a finished one; its verdict and its
	// error are in its round.json.
	r.logger.InfoContext(r.ctx, "round ended", "attack", request.Attack, "scenario", request.Scenario, "error", err)
}
