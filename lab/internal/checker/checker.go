// Package checker proves, from the history and the ledger export of a round,
// that the bank kept its invariants (docs/invariants-and-checker.md). This is
// version 1: I1 (I1a, I1b and the D term of I1c, per round), I2, the
// at-most-once part of I3 and I4.
package checker

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

// Version identifies the checker in every report.
const Version = "1"

const (
	historyFile  = "history.jsonl"
	manifestFile = "round.json"
	// maxLineBytes matches the history writer's largest record.
	maxLineBytes = 1 << 20
)

// Verdict is the outcome of a round.
type Verdict string

const (
	VerdictPass Verdict = "pass"
	VerdictFail Verdict = "fail"
)

// Status is the outcome of one invariant.
type Status string

const (
	StatusOK         Status = "ok"
	StatusFailed     Status = "failed"
	StatusNotChecked Status = "not_checked"
)

// InvariantResult is one row of the invariants table.
type InvariantResult struct {
	ID         Invariant `json:"id"`
	Status     Status    `json:"status"`
	Violations int       `json:"violations"`
	Note       string    `json:"note,omitempty"`
}

// OperationCounts summarizes how the round's operations ended.
type OperationCounts struct {
	Total int `json:"total"`
	OK    int `json:"ok"`
	Fail  int `json:"fail"`
	Info  int `json:"info"`
	Open  int `json:"open"`
}

// Result is the verdict and everything that supports it.
type Result struct {
	Verdict    Verdict
	Operations OperationCounts
	Invariants []InvariantResult
	// Violations are ordered by time; the first one is the earliest.
	Violations []Violation
	ops        operations
	ledger     ledger
}

// invariantsOfThisVersion lists every invariant with what version 1 covers.
var invariantsOfThisVersion = []struct {
	id        Invariant
	isChecked bool
	note      string
}{
	{id: InvariantI1, isChecked: true, note: "I1a e I1b completas; de I1c, só o termo D, por rodada"},
	{id: InvariantI2, isChecked: true},
	{id: InvariantI3, isChecked: true, note: "parcial: só \"no máximo uma vez\"; a convergência chega com o resolve (F5)"},
	{id: InvariantI4, isChecked: true},
	{id: "I5", note: "chega com o PIX de saída (F6)"},
	{id: "I6", note: "chega com o parceiro (F6)"},
	{id: "I7", note: "chega com a máquina de estados (F6)"},
	{id: "I8", note: "chega com as faixas de imutabilidade"},
}

// Evaluate checks a history against a ledger snapshot.
func Evaluate(records []history.Record, s snapshot.Snapshot) Result {
	e := evaluation{ops: indexOperations(records), ledger: indexLedger(s)}

	e.checkZeroSum()
	e.checkAccounts()
	e.checkDepositTerm()
	e.checkOperations()
	e.checkPhantoms()

	slices.SortStableFunc(e.violations, byTime)

	result := Result{Violations: e.violations, Operations: countOperations(e.ops), ops: e.ops, ledger: e.ledger}
	result.Invariants = invariantResults(e.violations)
	result.Verdict = VerdictPass

	if len(e.violations) > 0 {
		result.Verdict = VerdictFail
	}

	return result
}

// Check reads a round directory, evaluates it and writes report.json and
// report.md next to the history.
func Check(roundDir string) (Result, error) {
	records, err := readHistory(roundDir)
	if err != nil {
		return Result{}, err
	}

	s, err := snapshot.Read(roundDir)
	if err != nil {
		return Result{}, err
	}

	result := Evaluate(records, s)
	meta := readRoundMeta(roundDir, s.Meta.Round)

	err = writeReports(roundDir, newReport(meta, result))
	if err != nil {
		return Result{}, err
	}

	return result, nil
}

// byTime orders violations by when the operation behind them started;
// violations the history cannot place go last.
func byTime(a, b Violation) int {
	isAUnknown, isBUnknown := a.TimeNS == unknownTime, b.TimeNS == unknownTime
	if isAUnknown != isBUnknown {
		if isAUnknown {
			return 1
		}

		return -1
	}

	return cmp.Compare(a.TimeNS, b.TimeNS)
}

func countOperations(ops operations) OperationCounts {
	counts := OperationCounts{Total: len(ops.ordered)}

	for _, o := range ops.ordered {
		switch o.result {
		case history.TypeOK:
			counts.OK++
		case history.TypeFail:
			counts.Fail++
		case history.TypeInfo:
			counts.Info++
		default:
			counts.Open++
		}
	}

	return counts
}

func invariantResults(violations []Violation) []InvariantResult {
	perInvariant := map[Invariant]int{}
	for _, v := range violations {
		perInvariant[v.Invariant]++
	}

	results := make([]InvariantResult, 0, len(invariantsOfThisVersion))

	for _, invariant := range invariantsOfThisVersion {
		result := InvariantResult{ID: invariant.id, Status: StatusNotChecked, Note: invariant.note}

		if invariant.isChecked {
			result.Violations = perInvariant[invariant.id]
			result.Status = StatusOK
		}

		if result.Violations > 0 {
			result.Status = StatusFailed
		}

		results = append(results, result)
	}

	return results
}

func readHistory(roundDir string) ([]history.Record, error) {
	file, err := os.Open(filepath.Join(roundDir, historyFile)) //nolint:gosec // G304: the round directory comes from the operator.
	if err != nil {
		return nil, fmt.Errorf("opening history: %w", err)
	}
	defer file.Close()

	var records []history.Record

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	for scanner.Scan() {
		var record history.Record

		err := json.Unmarshal(scanner.Bytes(), &record)
		if err != nil {
			return nil, fmt.Errorf("history record %d is not JSON: %w", len(records)+1, err)
		}

		records = append(records, record)
	}

	err = scanner.Err()
	if err != nil {
		return nil, fmt.Errorf("reading history: %w", err)
	}

	return records, nil
}

// roundMeta is the part of round.json the report shows; round.json is
// optional, so the checker also works on a hand-made round directory.
type roundMeta struct {
	Round    string `json:"round"`
	Scenario string `json:"scenario"`
	Seed     string `json:"seed"`
	Duration string `json:"duration"`
}

func readRoundMeta(roundDir, round string) roundMeta {
	meta := roundMeta{Round: round}

	content, err := os.ReadFile(filepath.Join(roundDir, manifestFile)) //nolint:gosec // G304: the round directory comes from the operator.
	if err != nil {
		return meta
	}

	// A malformed round.json only costs the report its header fields.
	_ = json.Unmarshal(content, &meta)

	return meta
}
