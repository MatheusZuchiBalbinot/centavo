// Package checker proves, from the history and the ledger export of a round,
// that the bank kept its invariants, I1 to I8, each documented where it is
// declared.
package checker

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"centavo/lab/internal/history"
	"centavo/lab/internal/snapshot"
)

const Version = "1"

const (
	historyFile  = "history.jsonl"
	eventsFile   = "events.jsonl"
	truthFile    = "truth.json"
	manifestFile = "round.json"
	// maxLineBytes matches the history writer's largest record.
	maxLineBytes = 1 << 20
)

const (
	VerdictPass Verdict = "pass"
	VerdictFail Verdict = "fail"
	// VerdictInconclusive means no violation but some outcome stayed unknown.
	VerdictInconclusive Verdict = "inconclusive"
)

const (
	StatusOK         Status = "ok"
	StatusFailed     Status = "failed"
	StatusNotChecked Status = "not_checked"
)

var invariantsOfThisVersion = []struct {
	id        Invariant
	isChecked bool
	note      string
}{
	{id: InvariantI1, isChecked: true, note: noteI1},
	{id: InvariantI2, isChecked: true},
	{id: InvariantI3, isChecked: true, note: noteI3},
	{id: InvariantI4, isChecked: true},
	{id: InvariantI5, isChecked: true, note: noteI5},
	{id: InvariantI6, isChecked: true, note: noteI6},
	{id: InvariantI7, isChecked: true},
	{id: InvariantI8, isChecked: true, note: noteI8},
}

type Verdict string

type Status string

type InvariantResult struct {
	ID         Invariant `json:"id"`
	Status     Status    `json:"status"`
	Violations int       `json:"violations"`
	Note       string    `json:"note,omitempty"`
}

type OperationCounts struct {
	Total int `json:"total"`
	OK    int `json:"ok"`
	Fail  int `json:"fail"`
	Info  int `json:"info"`
	Open  int `json:"open"`
}

type Result struct {
	Verdict    Verdict
	Operations OperationCounts
	Invariants []InvariantResult
	// Violations are ordered by time; the first one is the earliest.
	Violations []Violation
	// Unresolved are indeterminate writes that resolve never answered.
	Unresolved []string
	ops        operations
	ledger     ledger
}

type outsideSources struct {
	truth    *PartnerTruth
	previous *snapshot.LedgerState
}

// They never decide an invariant; they only tell the story of a violation.
type chaosEvents map[string][]json.RawMessage

// round.json is optional, so the checker also works on a hand-made round
// directory.
type roundMeta struct {
	Round    string `json:"round"`
	Scenario string `json:"scenario"`
	Seed     string `json:"seed"`
	Duration string `json:"duration"`
}

func Evaluate(records []history.Record, state snapshot.Snapshot) Result {
	return EvaluateWithTruth(records, state, nil)
}

// Also compares the ledger with the partner's truth: I6, the R and S terms
// of I1c, and the authoritative part of I7.
func EvaluateWithTruth(records []history.Record, state snapshot.Snapshot, truth *PartnerTruth) Result {
	return evaluate(indexOperations(records), state, outsideSources{truth: truth})
}

func Check(roundDir string) (Result, error) {
	ops, err := readHistory(roundDir)
	if err != nil {
		return Result{}, err
	}

	err = ops.validate()
	if err != nil {
		return Result{}, err
	}

	state, err := snapshot.Read(roundDir)
	if err != nil {
		return Result{}, err
	}

	truth, err := readTruth(roundDir)
	if err != nil {
		return Result{}, err
	}

	meta := readRoundMeta(roundDir, state.Meta.Round)
	previous, err := readPreviousState(roundDir)
	if err != nil {
		return Result{}, err
	}

	result := evaluate(ops, state, outsideSources{truth: truth, previous: previous})
	events := readEvents(roundDir)

	histories, err := readHistoriesOf(roundDir, storiedOperations(result))
	if err != nil {
		return Result{}, err
	}

	report := newReport(meta, result, events, histories)
	report.ManualAttacks = ops.manualAttacks

	err = writeReports(roundDir, report)
	if err != nil {
		return Result{}, err
	}

	return result, nil
}

func evaluate(ops operations, state snapshot.Snapshot, sources outsideSources) Result {
	truth := sources.truth
	run := evaluation{ops: ops, ledger: indexLedger(state)}

	run.checkZeroSum()
	run.checkAccounts()
	run.checkDepositTerm()
	run.checkOperations()
	run.checkPhantoms()
	run.checkPix(truth)
	run.checkImmutability(sources.previous, state.Bands, state.Meta.FirstTransactionID)

	slices.SortStableFunc(run.violations, byTime)

	result := Result{Violations: run.violations, Unresolved: run.unresolved, Operations: countOperations(run.ops), ops: run.ops, ledger: run.ledger}
	result.Invariants = invariantResults(run.violations)
	result.Verdict = verdictOf(run.violations, run.unresolved)

	return result
}

func verdictOf(violations []Violation, unresolved []string) Verdict {
	if len(violations) > 0 {
		return VerdictFail
	}

	if len(unresolved) > 0 {
		return VerdictInconclusive
	}

	return VerdictPass
}

// Violations the history cannot place go last.
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
		// A result is a completion or empty, never an invoke or an attempt.
		//exhaustive:ignore
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

func readHistory(roundDir string) (operations, error) {
	ops := newOperations()

	err := scanHistory(roundDir, ops.add)
	if err != nil {
		return operations{}, err
	}

	ops.indexResolutions()

	return ops, nil
}

// Read again for the few operations the report shows.
func readHistoriesOf(roundDir string, wanted map[string]bool) (operationHistories, error) {
	histories := operationHistories{}
	if len(wanted) == 0 {
		return histories, nil
	}

	err := scanHistory(roundDir, func(record history.Record) {
		if wanted[record.Op] {
			histories[record.Op] = append(histories[record.Op], record)
		}
	})

	return histories, err
}

func scanHistory(roundDir string, take func(history.Record)) error {
	file, err := os.Open(filepath.Join(roundDir, historyFile)) //nolint:gosec // G304: the round directory comes from the operator.
	if err != nil {
		return fmt.Errorf("opening history: %w", err)
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)
	line := 0

	for scanner.Scan() {
		line++

		var record history.Record

		err := json.Unmarshal(scanner.Bytes(), &record)
		if err != nil {
			return fmt.Errorf("history record %d is not JSON: %w", line, err)
		}

		take(record)
	}

	err = scanner.Err()
	if err != nil {
		return fmt.Errorf("reading history: %w", err)
	}

	return nil
}

func readPreviousState(roundDir string) (*snapshot.LedgerState, error) {
	state, isFound, err := snapshot.ReadState(filepath.Join(roundDir, previousStateFile))
	if err != nil {
		return nil, err
	}

	if !isFound {
		return nil, nil
	}

	return &state, nil
}

// A round that never touched the partner has none.
func readTruth(roundDir string) (*PartnerTruth, error) {
	content, err := os.ReadFile(filepath.Join(roundDir, truthFile)) //nolint:gosec // G304: the round directory comes from the operator.
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading truth.json: %w", err)
	}

	var truth PartnerTruth

	err = json.Unmarshal(content, &truth)
	if err != nil {
		return nil, fmt.Errorf("truth.json is not the partner's truth: %w", err)
	}

	return &truth, nil
}

// A round without chaos has none.
func readEvents(roundDir string) chaosEvents {
	events := chaosEvents{}

	file, err := os.Open(filepath.Join(roundDir, eventsFile)) //nolint:gosec // G304: the round directory comes from the operator.
	if err != nil {
		return events
	}

	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	for scanner.Scan() {
		var event struct {
			CorrelationID string `json:"correlation_id"`
		}

		line := slices.Clone(scanner.Bytes())

		err := json.Unmarshal(line, &event)
		if err != nil {
			continue
		}

		if event.CorrelationID == "" {
			continue
		}

		events[event.CorrelationID] = append(events[event.CorrelationID], line)
	}

	return events
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
