package round

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAllocate_PicksTheNextIDAndIgnoresOtherEntries(t *testing.T) {
	roundsDir := t.TempDir()

	for _, name := range []string{"r0001", "r0041", "notes", "r12", "rabcd"} {
		err := os.Mkdir(filepath.Join(roundsDir, name), 0o750)
		if err != nil {
			t.Fatal(err)
		}
	}

	id, dir, err := allocate(roundsDir)
	if err != nil {
		t.Fatal(err)
	}

	if id != "r0042" || dir != filepath.Join(roundsDir, "r0042") {
		t.Fatalf("got %s in %s, want r0042", id, dir)
	}

	second, _, err := allocate(roundsDir)
	if err != nil || second != "r0043" {
		t.Fatalf("second allocation: %s, %v", second, err)
	}
}

func TestAllocate_CreatesTheRoundsDirectory(t *testing.T) {
	roundsDir := filepath.Join(t.TempDir(), "data", "rounds")

	id, _, err := allocate(roundsDir)
	if err != nil || id != "r0001" {
		t.Fatalf("got %s, %v", id, err)
	}
}
