package round

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"centavo/lab/internal/platform/fsmode"
)

// lockFile sits next to the rounds. Two rounds at once would share the bank,
// the accounts and the faults, and each would judge the other's chaos.
const lockFile = ".round.lock"

// ErrAnotherRound means a round is already running against this lab.
var ErrAnotherRound = errors.New("another round is already running")

// Lock holds the lab for one round until unlock. The kernel releases the lock
// when the process dies, so a crashed round never leaves the lab locked.
func Lock(roundsDir string) (func(), error) {
	err := os.MkdirAll(roundsDir, fsmode.PrivateDir)
	if err != nil {
		return nil, fmt.Errorf("creating rounds directory: %w", err)
	}

	path := filepath.Join(roundsDir, lockFile)

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fsmode.PrivateFile) //nolint:gosec // G304: a fixed name inside the configured rounds directory.
	if err != nil {
		return nil, fmt.Errorf("opening the round lock: %w", err)
	}

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return nil, errors.Join(ErrAnotherRound, file.Close())
	}

	if err != nil {
		return nil, errors.Join(fmt.Errorf("taking the round lock: %w", err), file.Close())
	}

	return func() { unlock(file) }, nil
}

// IsLocked reports whether a round holds the lab right now.
func IsLocked(roundsDir string) (bool, error) {
	unlock, err := Lock(roundsDir)
	if errors.Is(err, ErrAnotherRound) {
		return true, nil
	}

	if err != nil {
		return false, err
	}

	unlock()

	return false, nil
}

// unlock releases the lock; closing the file alone would release it too, so
// an error here leaves nothing held.
func unlock(file *os.File) {
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}
