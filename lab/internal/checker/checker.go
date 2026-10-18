// Package checker proves, from the history and the ledger export of a round,
// that the bank kept its invariants (docs/invariants-and-checker.md). This is
// version 1: I1 (I1a, I1b and the D term of I1c, per round), I2, the
// at-most-once part of I3 and I4.
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

// Version identifies the checker in every report.
const Version = "1"

const (
	historyFile  = "history.jsonl"
	eventsFile   = "events.jsonl"
	truthFile    = "truth.json"
	manifestFile = "round.json"
	// maxLineBytes matches the history writer's largest record.
	maxLineBytes = 1 << 20
)

// Verdict is the outcome of a round.
type Verdict string

// The verdicts of a round.
const (
	VerdictPass Verdict = "pass"
	VerdictFail Verdict = "fail"
	// VerdictInconclusive means no violation but some outcome stayed unknown.
	VerdictInconclusive Verdict = "inconclusive"
)

// Status is the outcome of one invariant.
type Status string

// The outcomes of one invariant.
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
	// Unresolved are indeterminate writes that resolve never answered.
	Unresolved []string
	ops        operations
	ledger     ledger
}

// invariantsOfThisVersion lists every invariant with what version 1 covers.
var invariantsOfThisVersion = []struct {
	id        Invariant
	isChecked bool
	note      string
}{
	{id: InvariantI1, isChecked: true, note: "I1a e I1b completas; I1c com os termos D, R e S, por rodada"},
	{id: InvariantI2, isChecked: true},
	{id: InvariantI3, isChecked: true, note: "no máximo uma vez, convergência pelo resolve e estabilidade numa amostra"},
	{id: InvariantI4, isChecked: true},
	{id: InvariantI5, isChecked: true, note: "transferências terminais e trânsito zero depois da quiescência"},
	{id: InvariantI6, isChecked: true, note: "comparação direta com a verdade do parceiro; a conciliação chega na F9"},
	{id: InvariantI7, isChecked: true},
	{id: InvariantI8, isChecked: true, note: "todas as faixas completas da rodada anterior, recalculadas a cada rodada"},
}

// Evaluate checks a history against a ledger snapshot, without the partner.
func Evaluate(records []history.Record, s snapshot.Snapshot) Result {
	return EvaluateWithTruth(records, s, nil)
}

// EvaluateWithTruth also compares the ledger with the partner's truth (I6,
// the R and S terms of I1c, the authoritative part of I7).
func EvaluateWithTruth(records []history.Record, s snapshot.Snapshot, truth *PartnerTruth) Result {
	return evaluate(indexOperations(records), s, outsideSources{truth: truth})
}

// outsideSources are what the checker compares the ledger with besides the
// history: the partner's truth and the previous round's ledger state.
type outsideSources struct {
	truth    *PartnerTruth
	previous *snapshot.LedgerState
}

func evaluate(ops operations, s snapshot.Snapshot, sources outsideSources) Result {
	truth := sources.truth
	e := evaluation{ops: ops, ledger: indexLedger(s)}

	e.checkZeroSum()
	e.checkAccounts()
	e.checkDepositTerm()
	e.checkOperations()
	e.checkPhantoms()
	e.checkPix(truth)
	e.checkImmutability(sources.previous, s.Bands, s.Meta.FirstTransactionID)

	slices.SortStableFunc(e.violations, byTime)

	result := Result{Violations: e.violations, Unresolved: e.unresolved, Operations: countOperations(e.ops), ops: e.ops, ledger: e.ledger}
	result.Invariants = invariantResults(e.violations)
	result.Verdict = verdictOf(e.violations, e.unresolved)

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

// Check reads a round directory, evaluates it and writes report.json and
// report.md next to the history.
func Check(roundDir string) (Result, error) {
	ops, err := readHistory(roundDir)
	if err != nil {
		return Result{}, err
	}

	err = ops.validate()
	if err != nil {
		return Result{}, err
	}

	s, err := snapshot.Read(roundDir)
	if err != nil {
		return Result{}, err
	}

	truth, err := readTruth(roundDir)
	if err != nil {
		return Result{}, err
	}

	meta := readRoundMeta(roundDir, s.Meta.Round)
	previous, err := readPreviousState(roundDir)
	if err != nil {
		return Result{}, err
	}

	result := evaluate(ops, s, outsideSources{truth: truth, previous: previous})
	events := readEvents(roundDir)

	histories, err := readHistoriesOf(roundDir, storiedOperations(result))
	if err != nil {
		return Result{}, err
	}

	err = writeReports(roundDir, newReport(meta, result, events, histories))
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

// readHistory streams the history into the operations index.
func readHistory(roundDir string) (operations, error) {
	ops := newOperations()

	err := scanHistory(roundDir, ops.add)
	if err != nil {
		return operations{}, err
	}

	ops.indexResolutions()

	return ops, nil
}

// readHistoriesOf reads the history again for the records of a few
// operations, the ones the report shows.
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

// readPreviousState loads the state the round before left, if any.
func readPreviousState(roundDir string) (*snapshot.LedgerState, error) {
	state, isFound, err := snapshot.ReadState(filepath.Join(roundDir, previousStateFile))
	if err != nil || !isFound {
		return nil, err
	}

	return &state, nil
}

// readTruth loads truth.json, the partner's record for the round; a round
// that never touched the partner has none.
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

// chaosEvents are the failpoint events of the round, by correlation id. They
// never decide an invariant; they only tell the story (section 2).
type chaosEvents map[string][]json.RawMessage

// readEvents loads events.jsonl when it exists; a round without chaos has none.
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
		if err != nil || event.CorrelationID == "" {
			continue
		}

		events[event.CorrelationID] = append(events[event.CorrelationID], line)
	}

	return events
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
