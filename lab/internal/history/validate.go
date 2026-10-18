package history

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

// maxLineBytes fits the largest record: a statement page of 200 entries.
const maxLineBytes = 1 << 20

// maxViolations keeps the report readable when a history is badly broken.
const maxViolations = 100

// Violation is one broken well-formedness rule.
type Violation struct {
	Index   int64
	Message string
}

// Report summarizes a history and lists what is wrong with it.
type Report struct {
	Records     int
	Invocations int
	OK          int
	Fail        int
	Info        int
	Open        []string
	Violations  []Violation
}

// IsWellFormed reports whether every rule of docs/invariants-and-checker.md,
// section 4.2, holds. Open operations are allowed: they are what a crashed lab
// leaves behind, and the caller decides whether they are acceptable.
func (r Report) IsWellFormed() bool {
	return len(r.Violations) == 0
}

type openOperation struct {
	process  Process
	function Function
}

type validator struct {
	report         Report
	round          string
	lastIndex      int64
	open           map[string]openOperation
	closed         map[string]bool
	busyProcesses  map[Process]string
	retiredProcess map[Process]bool
}

// Validate reads a whole history and checks its well-formedness.
func Validate(reader io.Reader) (Report, error) {
	v := validator{
		open: map[string]openOperation{}, closed: map[string]bool{}, busyProcesses: map[Process]string{}, retiredProcess: map[Process]bool{},
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	for scanner.Scan() {
		v.report.Records++
		v.check(scanner.Bytes())
	}

	err := scanner.Err()
	if err != nil {
		return Report{}, fmt.Errorf("reading history: %w", err)
	}

	for op := range v.open {
		v.report.Open = append(v.report.Open, op)
	}

	slices.Sort(v.report.Open)

	return v.report, nil
}

func (v *validator) check(line []byte) {
	var record Record

	err := json.Unmarshal(line, &record)
	if err != nil {
		v.violate(v.lastIndex, "line after index %d is not a JSON record", v.lastIndex)

		return
	}

	v.checkEnvelope(record)

	if record.Process.IsNemesis() {
		v.checkNemesis(record)

		return
	}

	switch record.Type {
	case TypeInvoke:
		v.checkInvoke(record)
	case TypeAttempt:
		v.checkAttempt(record)
	case TypeOK, TypeFail, TypeInfo:
		v.checkCompletion(record)
	default:
		v.violate(record.Index, "unknown record type %q", record.Type)
	}
}

func (v *validator) checkEnvelope(record Record) {
	if record.Index <= v.lastIndex {
		v.violate(record.Index, "index %d does not follow %d", record.Index, v.lastIndex)
	}

	v.lastIndex = max(v.lastIndex, record.Index)

	if v.round == "" {
		v.round = record.Round
	}

	if record.Round != v.round {
		v.violate(record.Index, "round %q differs from %q", record.Round, v.round)
	}
}

// checkNemesis accepts the chaos bot's records: always info, always naming
// what was done, never part of a client operation.
func (v *validator) checkNemesis(record Record) {
	isWellFormed := record.Type == TypeInfo && record.F != "" && record.Op == ""
	if !isWellFormed {
		v.violate(record.Index, "nemesis record must be an info with f and without op")
	}
}

func (v *validator) checkInvoke(record Record) {
	v.report.Invocations++

	_, isOpen := v.open[record.Op]
	isRepeated := isOpen || v.closed[record.Op]
	if isRepeated {
		v.violate(record.Index, "operation %s invoked twice", record.Op)

		return
	}

	if v.retiredProcess[record.Process] {
		v.violate(record.Index, "process %s invoked %s after it was retired by an info", record.Process, record.Op)
	}

	busyWith, isBusy := v.busyProcesses[record.Process]
	if isBusy {
		v.violate(record.Index, "process %s invoked %s while %s is still open", record.Process, record.Op, busyWith)
	}

	hasFunction := record.F != "" && record.Op != ""
	if !hasFunction {
		v.violate(record.Index, "invoke without f or op")
	}

	v.open[record.Op] = openOperation{process: record.Process, function: record.F}
	v.busyProcesses[record.Process] = record.Op
}

func (v *validator) checkAttempt(record Record) {
	operation, isOpen := v.open[record.Op]
	if !isOpen {
		v.violate(record.Index, "attempt of %s, which is not open", record.Op)

		return
	}

	if operation.process != record.Process {
		v.violate(record.Index, "attempt of %s from process %s, invoked by %s", record.Op, record.Process, operation.process)
	}
}

func (v *validator) checkCompletion(record Record) {
	operation, isOpen := v.open[record.Op]
	if !isOpen {
		v.violate(record.Index, "%s of %s, which is not open", record.Type, record.Op)

		return
	}

	if operation.process != record.Process {
		v.violate(record.Index, "%s of %s from process %s, invoked by %s", record.Type, record.Op, record.Process, operation.process)
	}

	if operation.function != record.F {
		v.violate(record.Index, "%s of %s says f=%s, invoked as %s", record.Type, record.Op, record.F, operation.function)
	}

	delete(v.open, record.Op)
	delete(v.busyProcesses, operation.process)
	v.closed[record.Op] = true
	v.count(record)
}

// count tallies a completion; an info retires its process (Jepsen's rule: after
// an indeterminate result the process is no longer sequential).
func (v *validator) count(record Record) {
	// Only completions are counted.
	//exhaustive:ignore
	switch record.Type {
	case TypeOK:
		v.report.OK++
	case TypeFail:
		v.report.Fail++
	case TypeInfo:
		v.report.Info++
		v.retiredProcess[record.Process] = true
	}
}

func (v *validator) violate(index int64, format string, args ...any) {
	if len(v.report.Violations) >= maxViolations {
		return
	}

	v.report.Violations = append(v.report.Violations, Violation{Index: index, Message: fmt.Sprintf(format, args...)})
}
