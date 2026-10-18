package checker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

// TestCheck_FailurePathKeepsTheOperationHistory covers the second read of
// the history: the index keeps no records, so the report must find them
// again in the file.
func TestCheck_FailurePathKeepsTheOperationHistory(t *testing.T) {
	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 1_000)
	lost := b.operation(history.FunctionTransfer, transferArgs{From: alice, To: bob, AmountCents: 300}, history.TypeOK, writeValue{TransactionID: "tx-never-written"})

	roundDir := t.TempDir()
	writeRound(t, roundDir, b.records, b.snapshot())

	result, err := Check(roundDir)
	if err != nil {
		t.Fatal(err)
	}

	if result.Verdict != VerdictFail {
		t.Fatalf("an acknowledged transfer missing from the ledger passed: %+v", result)
	}

	report := readReport(t, roundDir)
	if len(report.FailurePaths) == 0 {
		t.Fatal("the report has no failure path")
	}

	path := report.FailurePaths[0]
	if path.Violation.Op != lost {
		t.Fatalf("the first failure path is about %s, want %s", path.Violation.Op, lost)
	}

	isInvokeAndCompletion := len(path.History) == 2 && path.History[0].Op == lost && path.History[1].Type == history.TypeOK
	if !isInvokeAndCompletion {
		t.Fatalf("the failure path history is %+v, want the invoke and the ok of %s", path.History, lost)
	}
}

func writeRound(t *testing.T, roundDir string, records []history.Record, s snapshot.Snapshot) {
	t.Helper()

	file, err := os.Create(filepath.Join(roundDir, historyFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	encoder := json.NewEncoder(file)

	for _, record := range records {
		err := encoder.Encode(record)
		if err != nil {
			t.Fatal(err)
		}
	}

	err = snapshot.Write(roundDir, s)
	if err != nil {
		t.Fatal(err)
	}
}

func readReport(t *testing.T, roundDir string) Report {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(roundDir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}

	var report Report

	err = json.Unmarshal(content, &report)
	if err != nil {
		t.Fatal(err)
	}

	return report
}

// TestCheck_StopsOnAValueItCannotRead: an amount the checker cannot decode
// would count as zero in the deposit term and match no ledger entry. The
// check stops instead of judging the round on that.
func TestCheck_StopsOnAValueItCannotRead(t *testing.T) {
	b := newRoundBuilder(t)
	alice := b.createAccount()
	b.operation(history.FunctionDeposit, map[string]any{"account_id": alice, "amount_cents": "500"}, history.TypeOK, writeValue{TransactionID: b.upcomingTransaction()})

	roundDir := t.TempDir()
	writeRound(t, roundDir, b.records, b.snapshot())

	_, err := Check(roundDir)
	if !errors.Is(err, ErrMalformedHistory) {
		t.Fatalf("got %v, want ErrMalformedHistory", err)
	}
}
