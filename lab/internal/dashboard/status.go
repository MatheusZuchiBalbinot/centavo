package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"time"

	"centavo/lab/internal/audit"

	"centavo/lab/internal/checker"
)

// checkState is how the panel paints one check; the text says it too, so the
// state never depends on color alone.
type checkState string

const (
	stateOK       checkState = "ok"
	stateViolated checkState = "violated"
	stateUnknown  checkState = "unknown"
)

const reportJSONFile = "report.json"

var roundDirPattern = regexp.MustCompile(`^r(\d{4,})$`)

type statusResponse struct {
	Live      liveStatus `json:"live"`
	LastRound *lastRound `json:"last_round"`
}

type liveStatus struct {
	At     *time.Time    `json:"at"`
	Checks []checkStatus `json:"checks"`
}

type checkStatus struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	State  checkState `json:"state"`
	Detail string     `json:"detail"`
}

// lastRound is the part of report.json the panel shows.
type lastRound struct {
	Round      string          `json:"round"`
	Scenario   string          `json:"scenario"`
	Seed       string          `json:"seed"`
	Verdict    checker.Verdict `json:"verdict"`
	Operations struct {
		Total int `json:"total"`
		OK    int `json:"ok"`
		Fail  int `json:"fail"`
		Info  int `json:"info"`
	} `json:"operations"`
	Invariants []struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		Violations int    `json:"violations"`
		Note       string `json:"note"`
	} `json:"invariants"`
	ReportURL string `json:"report_url"`
}

// waitingStatus is the live panel before the auditor's first reading.
func waitingStatus() liveStatus {
	return liveStatus{Checks: unknownChecks("aguardando a primeira leitura")}
}

func liveStatusOf(reading audit.Reading) liveStatus {
	at := reading.At

	if reading.IsUnavailable {
		return liveStatus{At: &at, Checks: unknownChecks("o banco de dados não respondeu")}
	}

	return liveStatus{At: &at, Checks: []checkStatus{sumCheck(reading), floorCheck(reading)}}
}

func unknownChecks(detail string) []checkStatus {
	return []checkStatus{
		{ID: "I1", Label: "Conservação: soma de todos os saldos", State: stateUnknown, Detail: detail},
		{ID: "I4", Label: "Saldo de cliente nunca negativo", State: stateUnknown, Detail: detail},
	}
}

func sumCheck(reading audit.Reading) checkStatus {
	check := checkStatus{ID: "I1", Label: "Conservação: soma de todos os saldos", State: stateOK, Detail: "zero, como exige a partida dobrada"}

	if reading.SumCents != 0 {
		check.State = stateViolated
		check.Detail = fmt.Sprintf("%s: dinheiro criado ou destruído", formatCents(reading.SumCents))
	}

	return check
}

func floorCheck(reading audit.Reading) checkStatus {
	check := checkStatus{ID: "I4", Label: "Saldo de cliente nunca negativo", State: stateOK}

	if reading.MinCustomerCents == nil {
		check.Detail = "nenhuma conta de cliente ainda"

		return check
	}

	minimum := *reading.MinCustomerCents
	check.Detail = "menor saldo: " + formatCents(minimum)

	if minimum < 0 {
		check.State = stateViolated
	}

	return check
}

// latestRound reads report.json of the highest-numbered round that has one.
func latestRound(roundsDir string) (*lastRound, error) {
	entries, err := os.ReadDir(roundsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("listing rounds: %w", err)
	}

	for _, id := range roundsNewestFirst(entries) {
		content, err := os.ReadFile(filepath.Join(roundsDir, id, reportJSONFile)) //nolint:gosec // G304: id matched roundDirPattern.
		if err != nil {
			continue
		}

		var round lastRound

		err = json.Unmarshal(content, &round)
		if err != nil {
			return nil, fmt.Errorf("reading %s/%s: %w", id, reportJSONFile, err)
		}

		round.ReportURL = "/rounds/" + id + "/report.md"

		return &round, nil
	}

	return nil, nil
}

func roundsNewestFirst(entries []os.DirEntry) []string {
	type numbered struct {
		id     string
		number int
	}

	var rounds []numbered

	for _, entry := range entries {
		match := roundDirPattern.FindStringSubmatch(entry.Name())
		if match == nil || !entry.IsDir() {
			continue
		}

		number, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}

		rounds = append(rounds, numbered{id: entry.Name(), number: number})
	}

	slices.SortFunc(rounds, func(a, b numbered) int { return b.number - a.number })

	ids := make([]string, 0, len(rounds))
	for _, round := range rounds {
		ids = append(ids, round.id)
	}

	return ids
}

// formatCents prints R$ 1.234,56, the way the panel shows money.
func formatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}

	reais := strconv.FormatInt(cents/100, 10)

	var grouped []byte

	for i, digit := range []byte(reais) {
		isGroupStart := i > 0 && (len(reais)-i)%3 == 0
		if isGroupStart {
			grouped = append(grouped, '.')
		}

		grouped = append(grouped, digit)
	}

	return fmt.Sprintf("%sR$ %s,%02d", sign, grouped, cents%100)
}
