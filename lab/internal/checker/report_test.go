package checker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

// The index keeps no records, so the report must find them again in the
// file.
func TestCheck_FailurePathKeepsTheOperationHistory(t *testing.T) {
	t.Parallel()

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

// An amount the checker cannot decode would count as zero in the deposit
// term and match no ledger entry.
func TestCheck_StopsOnAValueItCannotRead(t *testing.T) {
	t.Parallel()

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

func writeRound(t *testing.T, roundDir string, records []history.Record, state snapshot.Snapshot) {
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

	err = snapshot.Write(roundDir, state)
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

// The seed replays the scenario's attacks, not the ones a person launched
// from the panel; the report says how many of those the round had.
func TestCheck_ReportCountsTheManualAttacksTheSeedWillNotRepeat(t *testing.T) {
	t.Parallel()

	b := newRoundBuilder(t)
	alice, bob := b.createAccount(), b.createAccount()
	b.deposit(alice, 1_000)
	b.nemesis(history.FunctionKill, map[string]string{"target": "bank-worker-1", "via": "manual"})
	b.nemesis(history.FunctionKill, map[string]string{"target": "bank-api", "via": "random"})
	b.nemesis(history.FunctionNetwork, map[string]any{"proxies": []string{"api_pg"}, "toxic": "blackhole", "phase": "start", "via": "manual"})
	b.nemesis(history.FunctionNetwork, map[string]any{"proxies": []string{"api_pg"}, "toxic": "blackhole", "phase": "end", "via": "manual"})
	b.operation(history.FunctionTransfer, transferArgs{From: alice, To: bob, AmountCents: 300}, history.TypeOK, writeValue{TransactionID: "tx-never-written"})

	roundDir := t.TempDir()
	writeRound(t, roundDir, b.records, b.snapshot())

	manifest := `{"round":"r0001","scenario":"random-kills","seed":"0x5eed","duration":"3m0s"}`

	err := os.WriteFile(filepath.Join(roundDir, manifestFile), []byte(manifest), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Check(roundDir)
	if err != nil {
		t.Fatal(err)
	}

	report := readReport(t, roundDir)

	isCounted := len(report.ManualAttacks) == 2 && report.ManualAttacks[history.FunctionKill] == 1 && report.ManualAttacks[history.FunctionNetwork] == 1
	if !isCounted {
		t.Fatalf("manual attacks %+v, want one kill and one network fault", report.ManualAttacks)
	}

	markdown, err := os.ReadFile(filepath.Join(roundDir, reportMDFile))
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"| Ataques manuais | 2 (queda 1, rede 1) |", "não os 2 ataques manuais desta rodada"} {
		if !strings.Contains(string(markdown), want) {
			t.Fatalf("report.md lacks %q:\n%s", want, markdown)
		}
	}
}
