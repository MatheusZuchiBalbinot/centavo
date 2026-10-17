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
	"runtime/debug"
	"strconv"
	"time"

	"centavo/lab/internal/checker"
	"centavo/lab/internal/docker"
	"centavo/lab/internal/export"
	"centavo/lab/internal/history"
	"centavo/lab/internal/loadgen"
	"centavo/lab/internal/nemesis"
	"centavo/lab/internal/seed"
	"centavo/lab/internal/snapshot"
	"centavo/lab/scenarios"
)

const (
	historyFile  = "history.jsonl"
	eventsFile   = "events.jsonl"
	truthFile    = "truth.json"
	manifestFile = "round.json"
	// maxAllocationTries bounds the race between two labs creating rounds.
	maxAllocationTries = 10
	// truthMargin starts the partner's export a little before the round, in
	// case the partner's clock runs behind the lab's.
	truthMargin = time.Minute
)

var roundDirPattern = regexp.MustCompile(`^r(\d{4,})$`)

var ErrNoRoundID = errors.New("could not allocate a round id")

// Options configure one round.
type Options struct {
	Scenario scenarios.Scenario
	Seed     seed.Seed
	BankURL  string
	// ControlURL is the bank's API without Toxiproxy, for resolve.
	ControlURL string
	RoundsDir  string
	LabVersion string
	Logger     *slog.Logger
	// Export reads the part of the ledger the round touched.
	Export func(ctx context.Context, request export.Request) (snapshot.Snapshot, error)
	// Check runs the checker on the archived round and returns its verdict.
	Check func(ctx context.Context, roundDir string) (checker.Verdict, error)
	// Chaos is how the nemesis reaches the targets.
	Chaos ChaosOptions
	// Saga is how the cure watches the PIX of the round settle.
	Saga SagaOptions
}

// SagaOptions read the state of the round's PIX for the cure.
type SagaOptions struct {
	InFlight        func(ctx context.Context, round string) (int, error)
	TransferIDs     func(ctx context.Context, round string) ([]string, error)
	PendingWebhooks func(ctx context.Context) (int, error)
	// Unreconciled counts the partner's statement entries the bank has not
	// booked yet: I1c holds only after the final reconciliation.
	Unreconciled func(ctx context.Context) (int64, error)
	// Truth reads the partner's truth from a time on.
	Truth func(ctx context.Context, since time.Time) ([]byte, error)
	// PartnerURL and ParticipantID are where the load asks for PIX in.
	PartnerURL    string
	ParticipantID string
}

// quiescencePoll is how often the cure looks at the sagas in flight.
const quiescencePoll = time.Second

// ChaosOptions are the nemesis's means of action.
type ChaosOptions struct {
	Docker    docker.Client
	Token     string
	EventsDir string
}

// Result is where a round was archived, what its history looks like and
// what the checker concluded.
type Result struct {
	ID        string
	Dir       string
	StartedAt time.Time
	History   history.Report
	Verdict   checker.Verdict
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

	bot := nemesis.New(nemesis.Config{
		Round:      id,
		Seed:       options.Seed,
		Scenario:   options.Scenario,
		History:    writer,
		Docker:     options.Chaos.Docker,
		ChaosToken: options.Chaos.Token,
		EventsDir:  options.Chaos.EventsDir,
		Logger:     options.Logger,

		PartnerAdminURL: options.Saga.PartnerURL,
		ParticipantID:   options.Saga.ParticipantID,
	})

	// A target that fails the preflight is never touched, not even to cure it.
	err = bot.Preflight(ctx)
	if err != nil {
		return Result{ID: id, Dir: dir}, errors.Join(err, writer.Close())
	}

	load, loadErr := runLoad(ctx, options, id, writer, bot)

	// The cure runs even after a failed load: nothing stays broken (lock 6).
	healErr := bot.Heal(ctx)
	if healErr == nil {
		healErr = cure(ctx, options, cureState{round: id, writer: writer, load: load})
	}
	closeErr := writer.Close()
	eventsErr := writeEvents(dir, bot.Events())
	runErr := errors.Join(loadErr, healErr, closeErr, eventsErr)

	report, err := validateHistory(dir)
	if err != nil {
		return Result{}, errors.Join(runErr, err)
	}

	result := Result{ID: id, Dir: dir, StartedAt: startedAt, History: report}

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

type cureState struct {
	round  string
	writer *history.Writer
	load   loadgen.Result
}

// cure is the end of the round after heal_complete (docs/chaos-bot.md,
// section 3): resolve every indeterminate write, wait for the sagas to
// settle until t_h + Δ, then read every PIX out once.
func cure(ctx context.Context, options Options, state cureState) error {
	resolveConfig := loadgen.ResolveConfig{Round: state.round, Seed: options.Seed, ControlURL: options.ControlURL, History: state.writer, Logger: options.Logger, Writes: state.load.Indeterminate, FirstProcess: state.load.NextProcess}

	next, err := loadgen.Resolve(ctx, resolveConfig)
	if err != nil {
		return err
	}

	if options.Saga.InFlight == nil {
		return nil
	}

	err = waitQuiescence(ctx, options, state.round)
	if err != nil {
		return err
	}

	ids, err := options.Saga.TransferIDs(ctx, state.round)
	if err != nil {
		return err
	}

	resolveConfig.FirstProcess = next

	return loadgen.ReadPixOuts(ctx, resolveConfig, ids)
}

// waitQuiescence waits until no PIX of the round is in flight and the
// partner has no webhook left, or the liveness deadline passes; then I5
// judges what is still open.
func waitQuiescence(ctx context.Context, options Options, round string) error {
	deadline := time.Now().Add(options.Scenario.LivenessDeadline)

	for time.Now().Before(deadline) {
		inFlight, err := options.Saga.InFlight(ctx, round)
		if err != nil {
			return err
		}

		pending, err := options.Saga.PendingWebhooks(ctx)
		if err != nil {
			options.Logger.WarnContext(ctx, "reading the partner's pending webhooks", "error", err)
		}

		unreconciled, errReconciled := options.Saga.Unreconciled(ctx)
		if errReconciled != nil {
			options.Logger.WarnContext(ctx, "reading how far the bank reconciled", "error", errReconciled)
		}

		isQuiet := inFlight == 0 && pending == 0 && unreconciled == 0 && err == nil && errReconciled == nil
		if isQuiet {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(quiescencePoll):
		}
	}

	options.Logger.WarnContext(ctx, "liveness deadline passed with sagas in flight", "round", round)

	return nil
}

func runLoad(ctx context.Context, options Options, id string, writer *history.Writer, bot *nemesis.Nemesis) (loadgen.Result, error) {
	loadConfig := loadgen.Config{
		Round:         id,
		Seed:          options.Seed,
		Workload:      options.Scenario.Workload,
		Duration:      options.Scenario.Duration,
		BankURL:       options.BankURL,
		History:       writer,
		Logger:        options.Logger,
		BeforeLoad:    bot.Start,
		PartnerURL:    options.Saga.PartnerURL,
		ParticipantID: options.Saga.ParticipantID,
	}

	return loadgen.Run(ctx, loadConfig)
}

// writeEvents keeps the failpoint events of the round next to its history;
// the checker uses them only to tell the story of a violation.
func writeEvents(dir string, events []json.RawMessage) error {
	var content []byte

	for _, event := range events {
		content = append(content, event...)
		content = append(content, '\n')
	}

	err := os.WriteFile(filepath.Join(dir, eventsFile), content, 0o600)
	if err != nil {
		return fmt.Errorf("writing events.jsonl: %w", err)
	}

	return nil
}

// exportAndCheck takes the ledger snapshot of the round and runs the checker on it.
func exportAndCheck(ctx context.Context, options Options, result Result) (checker.Verdict, error) {
	err := exportSnapshot(ctx, options, result)
	if err != nil {
		return "", err
	}

	if options.Saga.Truth != nil {
		truth, err := options.Saga.Truth(ctx, result.StartedAt.Add(-truthMargin))
		if err != nil {
			return "", err
		}

		err = os.WriteFile(filepath.Join(result.Dir, truthFile), truth, 0o600)
		if err != nil {
			return "", fmt.Errorf("writing truth.json: %w", err)
		}
	}

	// The snapshot is on disk now. The checker runs in this same cgroup and
	// loads it again, so the memory of this copy goes back to the system
	// first instead of waiting for the scavenger (docs/architecture.md,
	// section 11).
	debug.FreeOSMemory()

	return options.Check(ctx, result.Dir)
}

// exportSnapshot writes the ledger snapshot of the round to its directory.
func exportSnapshot(ctx context.Context, options Options, result Result) error {
	accounts, err := createdAccounts(result.Dir)
	if err != nil {
		return err
	}

	s, err := options.Export(ctx, export.Request{Round: result.ID, AccountIDs: accounts})
	if err != nil {
		return err
	}

	err = snapshot.Write(result.Dir, s)
	if err != nil {
		return err
	}

	options.Logger.InfoContext(ctx, "ledger exported", "round", result.ID, "transactions", len(s.Transactions), "entries", len(s.Entries))

	return nil
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
