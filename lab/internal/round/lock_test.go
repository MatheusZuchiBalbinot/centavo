package round

import (
	"errors"
	"testing"
)

func TestLock_RefusesASecondRoundUntilTheFirstUnlocks(t *testing.T) {
	dir := t.TempDir()

	unlock, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Lock(dir)
	if !errors.Is(err, ErrAnotherRound) {
		t.Fatalf("second lock: %v, want ErrAnotherRound", err)
	}

	isLocked, err := IsLocked(dir)
	if err != nil {
		t.Fatal(err)
	}

	if !isLocked {
		t.Fatal("IsLocked says free while a round holds the lock")
	}

	unlock()

	isLocked, err = IsLocked(dir)
	if err != nil {
		t.Fatal(err)
	}

	if isLocked {
		t.Fatal("IsLocked says held after the round unlocked")
	}
}
