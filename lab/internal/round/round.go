// Package round runs one round of a scenario and archives what it produced:
// history.jsonl and round.json (docs/chaos-bot.md, sections 3 and 15.2).
package round

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"centavo/lab/internal/checker"
	"centavo/lab/internal/export"
	"centavo/lab/internal/history"
	"centavo/lab/internal/loadgen"
	"centavo/lab/internal/seed"
	"centavo/lab/internal/snapshot"
	"centavo/lab/scenarios"
)

const (
	historyFile  = "history.jsonl"
	manifestFile = "round.json"
	// maxAllocationTries bounds the race between two labs creating rounds.
	maxAllocationTries = 10
)

var roundDirPattern = regexp.MustCompile(`^r(\d{4,})$`)

var ErrNoRoundID = errors.New("could not allocate a round id")

// Options configure one round.
type Options struct {
	Scenario   scenarios.Scenario
	Seed       seed.Seed
	BankURL    string
	RoundsDir  string
	LabVersion string
	Logger     *slog.Logger
	// Export reads the part of the ledger the round touched.
	Export func(ctx context.Context, request export.Request) (snapshot.Snapshot, error)
	// Check runs the checker on the archived round and returns its verdict.
	Check func(ctx context.Context, roundDir string) (checker.Verdict, error)
}

// Result is where a round was archived, what its history looks like and
// what the checker concluded.
type Result struct {
	ID      string
	Dir     string
	History history.Report
	Verdict checker.Verdict
}

// IsClean reports whether the history is well formed and every operation ended.
func (r Result) IsClean() bool {
	return r.History.IsWellFormed() && len(r.History.Open) == 0
}

type manifest struct {
	Round          string          `json:"round"`
	Scenario       string          `json:"scenario"`
	ScenarioSHA256 string          `json:"scenario_sha256"`
	Seed           string          `json:"seed"`
	Duration       string          `json:"duration"`
	LabVersion     string          `json:"lab_version"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     time.Time       `json:"finished_at"`
	History        historySummary  `json:"history"`
	Verdict        checker.Verdict `json:"verdict,omitempty"`
	Error          string          `json:"error,omitempty"`
	Workload       workloadSummary `json:"workload"`
}

type historySummary struct {
	Records      int  `json:"records"`
	Invocations  int  `json:"invocations"`
	OK           int  `json:"ok"`
	Fail         int  `json:"fail"`
	Info         int  `json:"info"`
	Open         int  `json:"open"`
	IsWellFormed bool `json:"well_formed"`
}

type workloadSummary struct {
	Profile           string  `json:"profile"`
	Clients           int     `json:"clients"`
	RatePerSecond     float64 `json:"rate_per_second"`
	Accounts          int     `json:"accounts"`
	ExclusiveAccounts bool    `json:"exclusive_accounts"`
}

// Run executes the round. A load error is returned after the round is
// archived, so a failed round still leaves its history behind.
func Run(ctx context.Context, options Options) (Result, error) {
	id, dir, err := allocate(options.RoundsDir)
	if err != nil {
		return Result{}, err
	}

	writer, err := history.Create(filepath.Join(dir, historyFile), id)
	if err != nil {
		return Result{}, err
	}

	startedAt := time.Now().UTC()
	options.Logger.InfoContext(ctx, "round started", "round", id, "scenario", options.Scenario.Name, "seed", options.Seed.String(), "dir", dir)

	loadConfig := loadgen.Config{
		Round:    id,
		Seed:     options.Seed,
		Workload: options.Scenario.Workload,
		Duration: options.Scenario.Duration,
		BankURL:  options.BankURL,
		History:  writer,
		Logger:   options.Logger,
	}

	loadErr := loadgen.Run(ctx, loadConfig)
	closeErr := writer.Close()
	runErr := errors.Join(loadErr, closeErr)

	report, err := validateHistory(dir)
	if err != nil {
		return Result{}, errors.Join(runErr, err)
	}

	result := Result{ID: id, Dir: dir, History: report}

	// The checker reads round.json for the report header, so it is written
	// before the check and again with the verdict.
	err = writeManifest(dir, newManifest(options, result, startedAt, runErr))
	if err != nil {
		return result, errors.Join(runErr, err)
	}

	if runErr != nil {
		return result, runErr
	}

	result.Verdict, runErr = exportAndCheck(ctx, options, result)

	err = writeManifest(dir, newManifest(options, result, startedAt, runErr))

	return result, errors.Join(runErr, err)
}

// exportAndCheck takes the ledger snapshot of the round and runs the checker on it.
func exportAndCheck(ctx context.Context, options Options, result Result) (checker.Verdict, error) {
	accounts, err := createdAccounts(result.Dir)
	if err != nil {
		return "", err
	}

	s, err := options.Export(ctx, export.Request{Round: result.ID, AccountIDs: accounts})
	if err != nil {
		return "", err
	}

	err = snapshot.Write(result.Dir, s)
	if err != nil {
		return "", err
	}

	options.Logger.InfoContext(ctx, "ledger exported", "round", result.ID, "transactions", len(s.Transactions), "entries", len(s.Entries))

	return options.Check(ctx, result.Dir)
}

// createdAccounts lists the accounts the history says the round opened: the
// export reads exactly those, so older rounds never leak into this verdict.
func createdAccounts(dir string) ([]string, error) {
	file, err := os.Open(filepath.Join(dir, historyFile)) //nolint:gosec // G304: dir comes from allocate, inside the configured rounds directory.
	if err != nil {
		return nil, fmt.Errorf("opening history: %w", err)
	}
	defer file.Close()

	var accounts []string

	decoder := json.NewDecoder(file)

	for decoder.More() {
		var record history.Record

		err := decoder.Decode(&record)
		if err != nil {
			return nil, fmt.Errorf("reading history: %w", err)
		}

		isCreated := record.Type == history.TypeOK && record.F == history.FunctionCreateAccount
		if !isCreated {
			continue
		}

		var created struct {
			AccountID string `json:"account_id"`
		}

		err = json.Unmarshal(record.Value, &created)
		if err != nil {
			return nil, fmt.Errorf("reading a created account: %w", err)
		}

		accounts = append(accounts, created.AccountID)
	}

	return accounts, nil
}

// allocate creates the next rNNNN directory; Mkdir failing on an existing
// directory makes two concurrent labs pick different ids.
func allocate(roundsDir string) (string, string, error) {
	err := os.MkdirAll(roundsDir, 0o750)
	if err != nil {
		return "", "", fmt.Errorf("creating rounds directory: %w", err)
	}

	for range maxAllocationTries {
		next, err := nextRoundNumber(roundsDir)
		if err != nil {
			return "", "", err
		}

		id := fmt.Sprintf("r%04d", next)
		dir := filepath.Join(roundsDir, id)

		err = os.Mkdir(dir, 0o750)
		if err == nil {
			return id, dir, nil
		}

		if !errors.Is(err, fs.ErrExist) {
			return "", "", fmt.Errorf("creating round directory: %w", err)
		}
	}

	return "", "", ErrNoRoundID
}

func nextRoundNumber(roundsDir string) (int, error) {
	entries, err := os.ReadDir(roundsDir)
	if err != nil {
		return 0, fmt.Errorf("listing rounds: %w", err)
	}

	highest := 0

	for _, entry := range entries {
		match := roundDirPattern.FindStringSubmatch(entry.Name())
		if match == nil {
			continue
		}

		number, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}

		highest = max(highest, number)
	}

	return highest + 1, nil
}

func validateHistory(dir string) (history.Report, error) {
	file, err := os.Open(filepath.Join(dir, historyFile)) //nolint:gosec // G304: dir comes from allocate, inside the configured rounds directory.
	if err != nil {
		return history.Report{}, fmt.Errorf("opening history: %w", err)
	}
	defer file.Close()

	return history.Validate(file)
}

func newManifest(options Options, result Result, startedAt time.Time, runErr error) manifest {
	report := result.History
	workload := options.Scenario.Workload

	roundManifest := manifest{
		Round:          result.ID,
		Scenario:       options.Scenario.Name,
		ScenarioSHA256: options.Scenario.SHA256,
		Seed:           options.Seed.String(),
		Duration:       options.Scenario.Duration.String(),
		LabVersion:     options.LabVersion,
		StartedAt:      startedAt,
		FinishedAt:     time.Now().UTC(),
		Verdict:        result.Verdict,
		History: historySummary{
			Records:      report.Records,
			Invocations:  report.Invocations,
			OK:           report.OK,
			Fail:         report.Fail,
			Info:         report.Info,
			Open:         len(report.Open),
			IsWellFormed: report.IsWellFormed(),
		},
		Workload: workloadSummary{
			Profile:           string(workload.Profile),
			Clients:           workload.Clients,
			RatePerSecond:     workload.RatePerSecond,
			Accounts:          workload.Accounts,
			ExclusiveAccounts: workload.ExclusiveAccounts,
		},
	}

	if runErr != nil {
		roundManifest.Error = runErr.Error()
	}

	return roundManifest
}

func writeManifest(dir string, roundManifest manifest) error {
	content, err := json.MarshalIndent(roundManifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding round.json: %w", err)
	}

	err = os.WriteFile(filepath.Join(dir, manifestFile), append(content, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("writing round.json: %w", err)
	}

	return nil
}
