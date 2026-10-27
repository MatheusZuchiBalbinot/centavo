// Package manual carries the attacks a person launches from the panel to the
// round that is running. The round runs in its own process, so the panel
// drops each command as a file in the round's commands directory, and the
// nemesis takes it from there. The directory exists only while the attack
// phase runs: once the cure starts, nothing more gets in.
package manual

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"centavo/lab/internal/platform/fsmode"
)

// DirName is the commands directory inside a round's directory.
const DirName = "commands"

const (
	Kill   Action = "kill"
	Freeze Action = "freeze"
	Cut    Action = "cut"
	Slow   Action = "slow"
)

// How long a manual fault lasts. Long enough to see the bank react, short
// enough that a person cannot hold the round down with a few clicks.
const (
	FreezeFor = 20 * time.Second
	CutFor    = 15 * time.Second
	SlowFor   = 20 * time.Second

	// SlowLatency is the delay a slow link adds to every answer, in
	// milliseconds, with SlowJitter around it.
	SlowLatency = 1_000
	SlowJitter  = 250
)

const (
	commandSuffix = ".json"
	pendingSuffix = ".tmp"
	maxCommand    = 1 << 10
)

var (
	ErrInvalid = errors.New("unknown manual attack or target")
	ErrClosed  = errors.New("the round is not under attack")
)

// processTargets can be killed or frozen; the auditor and the replica are
// not part of the system under attack.
var processTargets = []string{"bank-api", "bank-worker-1", "bank-worker-2", "partner", "postgres"}

// linkTargets can be cut or slowed. The auditor's link is the lab's own
// view of the bank, and the replication link exists only with the replica.
var linkTargets = []string{"client_api", "api_pg", "worker1_pg", "worker2_pg", "worker1_partner", "worker2_partner", "partner_webhooks"}

type Action string

type Command struct {
	Action Action `json:"action"`
	Target string `json:"target"`
}

func (c Command) Validate() error {
	switch c.Action {
	case Kill, Freeze:
		if slices.Contains(processTargets, c.Target) {
			return nil
		}
	case Cut, Slow:
		if slices.Contains(linkTargets, c.Target) {
			return nil
		}
	}

	return fmt.Errorf("%w: %s on %q", ErrInvalid, c.Action, c.Target)
}

// Open lets commands in for the round whose commands directory is dir.
func Open(dir string) error {
	err := os.MkdirAll(dir, fsmode.PrivateDir)
	if err != nil {
		return fmt.Errorf("opening the manual attacks: %w", err)
	}

	return nil
}

// Close stops letting commands in; whatever was not taken yet is dropped.
func Close(dir string) error {
	err := os.RemoveAll(dir)
	if err != nil {
		return fmt.Errorf("closing the manual attacks: %w", err)
	}

	return nil
}

// Send drops a command for the round. The file is written under another name
// and renamed, so the nemesis never reads half a command.
func Send(dir string, command Command) error {
	err := command.Validate()
	if err != nil {
		return err
	}

	_, err = os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrClosed
	}

	if err != nil {
		return fmt.Errorf("reading the commands directory: %w", err)
	}

	content, err := json.Marshal(command)
	if err != nil {
		return fmt.Errorf("encoding a manual attack: %w", err)
	}

	name := strconv.FormatInt(time.Now().UnixNano(), 10)
	pending := filepath.Join(dir, name+pendingSuffix)

	err = os.WriteFile(pending, content, fsmode.PrivateFile)
	if err != nil {
		return fmt.Errorf("writing a manual attack: %w", err)
	}

	err = os.Rename(pending, filepath.Join(dir, name+commandSuffix))
	if errors.Is(err, fs.ErrNotExist) {
		// The cure closed the directory between the check and the rename.
		return ErrClosed
	}

	if err != nil {
		return fmt.Errorf("writing a manual attack: %w", err)
	}

	return nil
}

// Take returns the commands waiting in dir, oldest first, and removes them.
// A command that does not decode or validate is dropped.
func Take(dir string) ([]Command, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the manual attacks: %w", err)
	}

	commands := []Command{}

	for _, entry := range entries {
		isCommand := !entry.IsDir() && strings.HasSuffix(entry.Name(), commandSuffix)
		if !isCommand {
			continue
		}

		command, isValid := readCommand(filepath.Join(dir, entry.Name()))
		if isValid {
			commands = append(commands, command)
		}
	}

	return commands, nil
}

func readCommand(path string) (Command, bool) {
	content, err := os.ReadFile(path) //nolint:gosec // G304: the path comes from listing the round's own commands directory.

	// Taken or not, a command runs at most once.
	_ = os.Remove(path)

	if err != nil || len(content) > maxCommand {
		return Command{}, false
	}

	var command Command

	err = json.Unmarshal(content, &command)
	if err != nil {
		return Command{}, false
	}

	return command, command.Validate() == nil
}
