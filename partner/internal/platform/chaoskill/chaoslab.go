//go:build chaoslab

package chaoskill

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

const procDir = "/proc"

var errNoMainProcess = errors.New("no main process found in /proc")

// KillMain sends SIGKILL to "<binary> serve", never to itself or to a healthcheck.
func KillMain(binary string) error {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return fmt.Errorf("listing processes: %w", err)
	}

	self := os.Getpid()

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		if pid == self {
			continue
		}

		cmdline, err := os.ReadFile(filepath.Join(procDir, entry.Name(), "cmdline"))
		if err != nil {
			continue
		}

		args := bytes.Split(bytes.TrimRight(cmdline, "\x00"), []byte{0})

		isMain := len(args) >= 2 && string(args[0]) == binary && string(args[1]) == "serve"
		if isMain {
			return syscall.Kill(pid, syscall.SIGKILL)
		}
	}

	return errNoMainProcess
}
