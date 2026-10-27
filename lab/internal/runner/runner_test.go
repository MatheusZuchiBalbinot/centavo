package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"centavo/lab/internal/attacks"
)

const roundEndsWithin = 5 * time.Second

type fakeProcess struct {
	mu        sync.Mutex
	stopCalls int
	finished  chan struct{}
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{finished: make(chan struct{})}
}

func (p *fakeProcess) StopAttack() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.stopCalls++

	return nil
}

func (p *fakeProcess) Wait() error {
	<-p.finished

	return nil
}

func (p *fakeProcess) finish() {
	close(p.finished)
}

func (p *fakeProcess) stops() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.stopCalls
}

var smokeRequest = Request{Attack: attacks.Calm, Scenario: "smoke", Duration: 3 * time.Minute}

func newTestRunner(t *testing.T, launch LaunchFunc) *Runner {
	t.Helper()

	return New(t.Context(), launch, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func launching(process *fakeProcess) LaunchFunc {
	return func(context.Context, Request) (Process, error) {
		return process, nil
	}
}

func waitForEnd(t *testing.T, runner *Runner, done <-chan struct{}) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(roundEndsWithin):
		t.Fatalf("the round did not end; state %+v", runner.State())
	}
}

func TestStart_RefusesASecondRoundWhileOneRuns(t *testing.T) {
	process := newFakeProcess()
	runner := newTestRunner(t, launching(process))

	state, err := runner.Start(smokeRequest)
	if err != nil {
		t.Fatal(err)
	}

	if state.Status != StatusRunning {
		t.Fatalf("status after start: %s", state.Status)
	}

	_, err = runner.Start(smokeRequest)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v, want ErrBusy", err)
	}

	done := runner.Done()
	process.finish()
	waitForEnd(t, runner, done)

	_, err = runner.Start(smokeRequest)
	if err != nil {
		t.Fatalf("start after the first round ended: %v", err)
	}
}

func TestStart_StaysIdleWhenTheLaunchFails(t *testing.T) {
	failing := func(context.Context, Request) (Process, error) {
		return nil, ErrBusy
	}
	runner := newTestRunner(t, failing)

	_, err := runner.Start(smokeRequest)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("start: %v, want the launch error", err)
	}

	if runner.State().Status != StatusIdle {
		t.Fatalf("status after a failed launch: %s", runner.State().Status)
	}
}

func TestStop_EndsTheAttackOnceAndIgnoresARepeat(t *testing.T) {
	process := newFakeProcess()
	runner := newTestRunner(t, launching(process))

	_, err := runner.Start(smokeRequest)
	if err != nil {
		t.Fatal(err)
	}

	state, err := runner.Stop()
	if err != nil {
		t.Fatal(err)
	}

	if state.Status != StatusStopping {
		t.Fatalf("status after stop: %s", state.Status)
	}

	_, err = runner.Stop()
	if err != nil {
		t.Fatalf("second stop: %v", err)
	}

	if process.stops() != 1 {
		t.Fatalf("the attack was asked to stop %d times", process.stops())
	}

	done := runner.Done()
	process.finish()
	waitForEnd(t, runner, done)

	if runner.State().Status != StatusIdle {
		t.Fatalf("status after the round ended: %s", runner.State().Status)
	}
}

func TestStop_WithoutARoundIsAnError(t *testing.T) {
	runner := newTestRunner(t, launching(newFakeProcess()))

	_, err := runner.Stop()
	if !errors.Is(err, ErrIdle) {
		t.Fatalf("stop when idle: %v, want ErrIdle", err)
	}
}
