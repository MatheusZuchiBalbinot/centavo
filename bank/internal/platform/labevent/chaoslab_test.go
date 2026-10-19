//go:build chaoslab

package labevent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRecord_AppendsOneLinePerDefenseToThisInstanceFile(t *testing.T) {
	dir := t.TempDir()
	Configure(Config{Dir: dir, Service: "bank-worker", Instance: "bank-worker-1"})
	t.Cleanup(func() {
		Configure(Config{})
		recorder.file = nil
	})

	Record(t.Context(), OutboxResumed, "pix_out", 3)
	Record(t.Context(), FencingRefused, "reconciler", 1)

	content, err := os.ReadFile(filepath.Join(dir, "bank-worker-1"+fileSuffix))
	if err != nil {
		t.Fatal(err)
	}

	var first event

	lines := splitLines(content)
	if len(lines) != 2 {
		t.Fatalf("got %d lines: %s", len(lines), content)
	}

	err = json.Unmarshal(lines[0], &first)
	if err != nil {
		t.Fatal(err)
	}

	isRecorded := first.Defense == OutboxResumed && first.Count == 3 && first.Instance == "bank-worker-1" && first.Detail == "pix_out"
	if !isRecorded {
		t.Fatalf("first event: %+v", first)
	}
}

func TestRecord_DoesNothingUntilConfigured(t *testing.T) {
	Configure(Config{})

	Record(t.Context(), IdempotentReplay, "", 1)

	if recorder.file != nil {
		t.Fatal("an unconfigured recorder opened a file")
	}
}

func splitLines(content []byte) [][]byte {
	return bytes.Split(bytes.TrimSuffix(content, []byte{'\n'}), []byte{'\n'})
}

func TestRecordLease_TellsANewHoldingOnceAndNotItsRenewals(t *testing.T) {
	dir := t.TempDir()
	Configure(Config{Dir: dir, Service: "bank-worker", Instance: "bank-worker-2"})
	t.Cleanup(func() {
		Configure(Config{})
		recorder.file = nil
		recorder.tokens = nil
	})

	RecordLease(t.Context(), "reconciler", 7)
	RecordLease(t.Context(), "reconciler", 7)
	RecordLease(t.Context(), "reconciler", 9)

	content, err := os.ReadFile(filepath.Join(dir, "bank-worker-2"+fileSuffix))
	if err != nil {
		t.Fatal(err)
	}

	if lines := splitLines(content); len(lines) != 2 {
		t.Fatalf("got %d lease events, want 2: %s", len(lines), content)
	}
}
