package checker

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"centavo/lab/internal/history"
)

const (
	reportJSONFile = "report.json"
	reportMDFile   = "report.md"
	// maxReportedViolations keeps report.json bounded; the count stays exact.
	maxReportedViolations = 1_000
	// maxFailurePaths is how many violations get their story told.
	maxFailurePaths = 10
	// maxPathLines bounds one story (docs/invariants-and-checker.md, section 9).
	maxPathLines = 20
)

// Report is report.json.
type Report struct {
	Round            string            `json:"round"`
	Scenario         string            `json:"scenario,omitempty"`
	Seed             string            `json:"seed,omitempty"`
	Duration         string            `json:"duration,omitempty"`
	Verdict          Verdict           `json:"verdict"`
	CheckerVersion   string            `json:"checker_version"`
	Operations       OperationCounts   `json:"operations"`
	Invariants       []InvariantResult `json:"invariants"`
	ViolationCount   int               `json:"violation_count"`
	Unresolved       []string          `json:"unresolved"`
	ViolationsByRule map[Rule]int      `json:"violations_by_rule"`
	ExampleByRule    map[Rule]string   `json:"example_by_rule"`
	Violations       []Violation       `json:"violations"`
	FailurePaths     []FailurePath     `json:"failure_paths"`
}

// FailurePath tells the story of one violation: the operation's history and
// the ledger rows it touched.
type FailurePath struct {
	Violation Violation         `json:"violation"`
	History   []history.Record  `json:"history"`
	Events    []json.RawMessage `json:"events"`
	Ledger    []string          `json:"ledger"`
}

func newReport(meta roundMeta, result Result, events chaosEvents) Report {
	byRule := map[Rule]int{}
	examples := map[Rule]string{}

	for _, v := range result.Violations {
		byRule[v.Rule]++

		_, hasExample := examples[v.Rule]
		if !hasExample {
			examples[v.Rule] = exampleOf(v)
		}
	}

	reported := result.Violations[:min(len(result.Violations), maxReportedViolations)]

	report := Report{
		Round:            meta.Round,
		Scenario:         meta.Scenario,
		Seed:             meta.Seed,
		Duration:         meta.Duration,
		Verdict:          result.Verdict,
		CheckerVersion:   Version,
		Operations:       result.Operations,
		Invariants:       result.Invariants,
		ViolationCount:   len(result.Violations),
		Unresolved:       append([]string{}, result.Unresolved...),
		ViolationsByRule: byRule,
		ExampleByRule:    examples,
		Violations:       slices.Clone(reported),
	}

	for _, v := range result.Violations[:min(len(result.Violations), maxFailurePaths)] {
		path := failurePathOf(result, v)
		path.Events = append([]json.RawMessage{}, events[v.Op]...)
		report.FailurePaths = append(report.FailurePaths, path)
	}

	return report
}

func failurePathOf(result Result, v Violation) FailurePath {
	path := FailurePath{Violation: v, Ledger: []string{}}

	o, isKnown := result.ops.byID[v.Op]
	if isKnown {
		path.History = o.records
	}

	transactionIDs := []string{}
	for _, transaction := range result.ledger.byClientRef[v.Op] {
		transactionIDs = append(transactionIDs, transaction.ID)
	}

	isExtraTransaction := v.TransactionID != "" && !slices.Contains(transactionIDs, v.TransactionID)
	if isExtraTransaction {
		transactionIDs = append(transactionIDs, v.TransactionID)
	}

	for _, id := range transactionIDs {
		transaction := result.ledger.transactions[id]
		path.Ledger = append(path.Ledger, fmt.Sprintf("ledger_transactions %s kind=%s client_ref=%s", id, transaction.Kind, transaction.ClientRef))

		for _, entry := range result.ledger.entriesByTx[id] {
			path.Ledger = append(path.Ledger, fmt.Sprintf("ledger_entries id=%d conta=%s valor=%d account_seq=%d balance_after=%d", entry.ID, entry.AccountID, entry.AmountCents, entry.AccountSeq, entry.BalanceAfterCents))
		}
	}

	balance, hasBalance := result.ledger.balances[v.AccountID]
	if hasBalance {
		path.Ledger = append(path.Ledger, fmt.Sprintf("account_balances conta=%s saldo=%d last_entry_seq=%d", v.AccountID, balance.BalanceCents, balance.LastEntrySeq))
	}

	path.Ledger = path.Ledger[:min(len(path.Ledger), maxPathLines)]

	return path
}

func writeReports(roundDir string, report Report) error {
	content, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding report.json: %w", err)
	}

	err = os.WriteFile(filepath.Join(roundDir, reportJSONFile), append(content, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("writing report.json: %w", err)
	}

	err = os.WriteFile(filepath.Join(roundDir, reportMDFile), []byte(renderMarkdown(report)), 0o600)
	if err != nil {
		return fmt.Errorf("writing report.md: %w", err)
	}

	return nil
}

// renderMarkdown writes report.md: text in Portuguese, invariant names and
// codes in English (docs/invariants-and-checker.md, section 11).
func renderMarkdown(report Report) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Rodada %s: %s\n\n", report.Round, verdictTitle(report))

	ops := report.Operations
	b.WriteString("| Campo | Valor |\n| --- | --- |\n")
	fmt.Fprintf(&b, "| Cenário | %s |\n", orDash(report.Scenario))
	fmt.Fprintf(&b, "| Semente | %s |\n", orDash(report.Seed))
	fmt.Fprintf(&b, "| Duração | %s |\n", orDash(report.Duration))
	fmt.Fprintf(&b, "| Operações | %d (ok %d, fail %d, info %d, abertas %d) |\n", ops.Total, ops.OK, ops.Fail, ops.Info, ops.Open)
	fmt.Fprintf(&b, "| Verificador | versão %s |\n", report.CheckerVersion)

	if len(report.FailurePaths) > 0 {
		writeFirstViolation(&b, report)
		writeViolationSummary(&b, report)
	}

	b.WriteString("\n## Invariantes\n\n| Id | Resultado | Observação |\n| --- | --- | --- |\n")

	for _, invariant := range report.Invariants {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", invariant.ID, statusText(invariant), invariant.Note)
	}

	return b.String()
}

func writeFirstViolation(b *strings.Builder, report Report) {
	path := report.FailurePaths[0]
	v := path.Violation

	fmt.Fprintf(b, "\n## Primeira violação: %s, %s\n\n%s\n\n", v.Invariant, v.Rule, v.Message)

	if v.Op != "" {
		fmt.Fprintf(b, "Operação %s, iniciada em %s.\n", v.Op, formatTime(v.TimeNS))
	}

	if len(path.History) > 0 {
		b.WriteString("\n### Histórico da operação\n\n| t | Registro | Detalhe |\n| --- | --- | --- |\n")

		for _, record := range path.History {
			fmt.Fprintf(b, "| %s | %s | %s |\n", formatTime(record.TimeNS), record.Type, recordDetail(record))
		}
	}

	if len(path.Events) > 0 {
		b.WriteString("\n### Falhas injetadas nesta operação\n\n")

		for _, event := range path.Events {
			fmt.Fprintf(b, "- `%s`\n", event)
		}
	}

	if len(path.Ledger) > 0 {
		b.WriteString("\n### Linhas do ledger\n\n")

		for _, line := range path.Ledger {
			fmt.Fprintf(b, "- `%s`\n", line)
		}
	}

	if report.Scenario != "" {
		fmt.Fprintf(b, "\n### Reproduzir\n\n```text\nlab run --scenario %s --seed %s --duration %s\n```\n", report.Scenario, report.Seed, report.Duration)
	}
}

func writeViolationSummary(b *strings.Builder, report Report) {
	b.WriteString("\n## Resumo das violações\n\n| Regra | Quantidade | Exemplo |\n| --- | --- | --- |\n")

	for _, rule := range slices.Sorted(maps.Keys(report.ViolationsByRule)) {
		fmt.Fprintf(b, "| %s | %d | %s |\n", rule, report.ViolationsByRule[rule], orDash(report.ExampleByRule[rule]))
	}
}

func verdictTitle(report Report) string {
	if report.Verdict == VerdictPass {
		return "PASSOU"
	}

	if report.Verdict == VerdictInconclusive {
		return fmt.Sprintf("INCONCLUSIVA (%d operações sem resolve)", len(report.Unresolved))
	}

	var failed []string
	for _, invariant := range report.Invariants {
		if invariant.Status == StatusFailed {
			failed = append(failed, string(invariant.ID))
		}
	}

	return fmt.Sprintf("FALHOU (%s)", strings.Join(failed, ", "))
}

func statusText(invariant InvariantResult) string {
	switch invariant.Status {
	case StatusOK:
		return "ok"
	case StatusFailed:
		return fmt.Sprintf("falhou (%d)", invariant.Violations)
	default:
		return "não verificada"
	}
}

func exampleOf(v Violation) string {
	if v.Op != "" {
		return v.Op
	}

	return v.AccountID
}

func recordDetail(record history.Record) string {
	switch {
	case len(record.Error) > 0:
		return "`" + string(record.Error) + "`"
	case len(record.Outcome) > 0:
		return fmt.Sprintf("tentativa %d: `%s`", record.N, record.Outcome)
	case len(record.Value) > 0:
		return "`" + string(record.Value) + "`"
	default:
		return ""
	}
}

// formatTime prints a round time like +312,4s, with the decimal comma of the
// Portuguese report.
func formatTime(timeNS int64) string {
	if timeNS == unknownTime {
		return "antes da rodada"
	}

	seconds := fmt.Sprintf("%.1f", float64(timeNS)/1e9)

	return "+" + strings.Replace(seconds, ".", ",", 1) + "s"
}

func orDash(value string) string {
	if value == "" {
		return "-"
	}

	return value
}
