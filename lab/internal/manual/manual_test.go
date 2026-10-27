package manual_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"centavo/lab/internal/manual"
)

func TestSend_ReachesTheRoundInOrderAndOnlyOnce(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), manual.DirName)

	err := manual.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	sent := []manual.Command{{Action: manual.Kill, Target: "bank-worker-1"}, {Action: manual.Cut, Target: "api_pg"}}

	for _, command := range sent {
		err := manual.Send(dir, command)
		if err != nil {
			t.Fatal(err)
		}
	}

	taken, err := manual.Take(dir)
	if err != nil {
		t.Fatal(err)
	}

	isInOrder := len(taken) == 2 && taken[0] == sent[0] && taken[1] == sent[1]
	if !isInOrder {
		t.Fatalf("taken %v, want %v", taken, sent)
	}

	again, err := manual.Take(dir)
	if err != nil || len(again) != 0 {
		t.Fatalf("a command ran twice: %v, %v", again, err)
	}
}

func TestSend_RefusesWhenTheAttackIsOver(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), manual.DirName)

	err := manual.Send(dir, manual.Command{Action: manual.Freeze, Target: "partner"})
	if !errors.Is(err, manual.ErrClosed) {
		t.Fatalf("before Open: %v, want ErrClosed", err)
	}

	err = manual.Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = manual.Close(dir)
	if err != nil {
		t.Fatal(err)
	}

	err = manual.Send(dir, manual.Command{Action: manual.Freeze, Target: "partner"})
	if !errors.Is(err, manual.ErrClosed) {
		t.Fatalf("after Close: %v, want ErrClosed", err)
	}
}

func TestValidate_KeepsEachActionToItsTargets(t *testing.T) {
	t.Parallel()

	refused := []manual.Command{
		{Action: manual.Kill, Target: "api_pg"},
		{Action: manual.Cut, Target: "bank-api"},
		{Action: manual.Kill, Target: "lab"},
		{Action: manual.Cut, Target: "auditor_pg"},
		{Action: "rm -rf", Target: "bank-api"},
	}

	for _, command := range refused {
		if !errors.Is(command.Validate(), manual.ErrInvalid) {
			t.Fatalf("%v was accepted", command)
		}
	}
}

func TestTake_DropsWhatDoesNotDecode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	err := os.WriteFile(filepath.Join(dir, "1.json"), []byte(`{"action":"kill","target":"lab"}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(dir, "2.json"), []byte(`not json`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	taken, err := manual.Take(dir)
	if err != nil || len(taken) != 0 {
		t.Fatalf("taken %v, %v", taken, err)
	}

	left, _ := os.ReadDir(dir)
	if len(left) != 0 {
		t.Fatalf("left behind %d files", len(left))
	}
}
