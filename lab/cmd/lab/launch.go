package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"centavo/lab/internal/round"
	"centavo/lab/internal/runner"
)

// shutdownGrace is how long a round may keep curing the bank after the panel
// asks it to stop because the panel itself is shutting down.
const shutdownGrace = 30 * time.Second

// roundLauncher starts the rounds the panel asks for as `lab run`
// subprocesses, so a round that runs out of memory never takes the panel
// with it.
type roundLauncher struct {
	roundsDir string
}

// roundProcess is a running `lab run`.
type roundProcess struct {
	command *exec.Cmd
}

func (l roundLauncher) launch(ctx context.Context, request runner.Request) (runner.Process, error) {
	// A round started from a terminal holds the same lock; checking it here
	// turns that case into a clear "busy" instead of a round that dies at once.
	isLocked, err := round.IsLocked(l.roundsDir)
	if err != nil {
		return nil, err
	}

	if isLocked {
		return nil, runner.ErrBusy
	}

	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("finding the lab binary: %w", err)
	}

	arguments := []string{commandRun, "--scenario", request.Scenario, "--duration", request.Duration.String()}

	command := exec.CommandContext(ctx, executable, arguments...) //nolint:gosec // G204: its own binary; the scenario comes from the attack menu.
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.WaitDelay = shutdownGrace
	// When the panel shuts down, the round ends its attack and cures the
	// bank instead of dying with its faults still on.
	command.Cancel = func() error { return command.Process.Signal(attackStopSignal) }

	err = command.Start()
	if err != nil {
		return nil, fmt.Errorf("starting lab run: %w", err)
	}

	return roundProcess{command: command}, nil
}

func (p roundProcess) StopAttack() error {
	err := p.command.Process.Signal(attackStopSignal)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}

	return err
}

func (p roundProcess) Wait() error {
	return p.command.Wait()
}
