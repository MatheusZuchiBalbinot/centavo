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

// Never to itself or to a healthcheck.
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

		path := filepath.Join(procDir, entry.Name(), "cmdline")

		cmdline, err := os.ReadFile(path) //nolint:gosec // G304: /proc/<pid>/cmdline of a listed process.
		if err != nil {
			continue
		}

		fields := bytes.Split(bytes.TrimRight(cmdline, "\x00"), []byte{0})

		isMain := len(fields) >= 2 && string(fields[0]) == binary && string(fields[1]) == "serve"
		if isMain {
			return syscall.Kill(pid, syscall.SIGKILL)
		}
	}

	return errNoMainProcess
}
