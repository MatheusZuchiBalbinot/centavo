// Command lab runs the chaos laboratory: load generator, chaos bot, auditor,
// checker and panel. It never imports code from bank or partner.
//
//	lab serve        panel (:8090) with the live auditor
//	lab run          one round: lab run --scenario smoke [--seed 0x5eed] [--duration 10m]
//	lab check        the checker alone: lab check --round /data/rounds/r0001
//	lab healthcheck  probes the local panel port; used by the container healthcheck
//
// lab check exits 0 when the round passes, 1 when it fails and 3 when the
// checker itself could not finish (2 is reserved for inconclusive rounds).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/lab/internal/audit"
	"centavo/lab/internal/checker"
	"centavo/lab/internal/dashboard"
	"centavo/lab/internal/docker"
	"centavo/lab/internal/export"
	"centavo/lab/internal/platform/buildinfo"
	"centavo/lab/internal/platform/config"
	"centavo/lab/internal/platform/httpserver"
	"centavo/lab/internal/round"
	"centavo/lab/internal/seed"
	"centavo/lab/internal/snapshot"
	"centavo/lab/scenarios"
)

const (
	commandServe       = "serve"
	commandRun         = "run"
	commandCheck       = "check"
	commandHealthcheck = "healthcheck"
)

const (
	panelAddr          = ":8090"
	healthcheckURL     = "http://127.0.0.1:8090" + dashboard.HealthPath
	healthcheckTimeout = 3 * time.Second
	// Pool sizes of docs/architecture.md, section 11.1.
	auditorConnections = 2
	exportConnections  = 2
	// eventsDir is the shared volume where services record fired failpoints.
	eventsDir = "/lab/events"
	// checkerMemoryLimit is the soft limit of the checker subprocess.
	checkerMemoryLimit = "GOMEMLIMIT=256MiB"
)

// Exit codes; lab check uses the checker's own (docs/invariants-and-checker.md, section 7).
const (
	exitOK                = 0
	exitError             = 1
	exitUsage             = 2
	exitRoundFailed       = 1
	exitRoundInconclusive = 2
	exitCheckerError      = 3
)

const usage = "usage: lab serve | run --scenario <name> [--seed <seed>] [--duration <d>] | check --round <dir> | healthcheck"

var (
	errUnhealthy         = errors.New("health endpoint did not answer 200")
	errUsage             = errors.New(usage)
	errRoundIsDirty      = errors.New("the round history is malformed or has open operations")
	errRoundFailed       = errors.New("the round broke invariants; see report.md")
	errRoundInconclusive = errors.New("the round is inconclusive: some outcomes stayed unknown; see report.md")
	errCheckerCrashed    = errors.New("the checker could not finish")
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, usage)

		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := runCommand(ctx, args[0], args[1:])
	if err == nil {
		return exitOK
	}

	fmt.Fprintf(os.Stderr, "lab %s: %v\n", args[0], err)

	return exitCodeOf(args[0], err)
}

func exitCodeOf(command string, err error) int {
	switch {
	case errors.Is(err, errUsage):
		fmt.Fprintln(os.Stderr, usage)

		return exitUsage
	case errors.Is(err, errRoundFailed):
		return exitRoundFailed
	case errors.Is(err, errRoundInconclusive):
		return exitRoundInconclusive
	case command == commandCheck, errors.Is(err, errCheckerCrashed):
		return exitCheckerError
	default:
		return exitError
	}
}

func runCommand(ctx context.Context, command string, args []string) error {
	switch command {
	case commandServe:
		return serve(ctx)
	case commandRun:
		return runRound(ctx, args)
	case commandCheck:
		return runCheck(args)
	case commandHealthcheck:
		return probeHealth(ctx)
	default:
		return errUsage
	}
}

func serve(ctx context.Context) error {
	serveConfig, err := config.LoadServe(os.LookupEnv)
	if err != nil {
		return err
	}

	logger := newLogger()

	pool, err := newPool(ctx, serveConfig.AuditorURL, auditorConnections)
	if err != nil {
		return err
	}
	defer pool.Close()

	auditor := audit.New(pool, logger)
	go auditor.Run(ctx)

	panelConfig := dashboard.Config{Version: buildinfo.Version(), Live: auditor.Latest, RoundsDir: serveConfig.RoundsDir, Logger: logger}
	panelServer := httpserver.New(panelAddr, dashboard.NewHandler(panelConfig))

	logger.InfoContext(ctx, "serving", "panel_addr", panelAddr)

	return httpserver.Run(ctx, panelServer)
}

// newPool connects lazily: the database may be down when the lab starts.
func newPool(ctx context.Context, databaseURL string, connections int32) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		// pgx errors can quote the URL, which carries the auditor password.
		return nil, config.ErrInvalidAuditor
	}

	poolConfig.MaxConns = connections

	return pgxpool.NewWithConfig(ctx, poolConfig)
}

type runFlags struct {
	scenario string
	seed     string
	duration time.Duration
}

func parseRunFlags(args []string) (runFlags, error) {
	var parsed runFlags

	flags := flag.NewFlagSet(commandRun, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&parsed.scenario, "scenario", "", "scenario name")
	flags.StringVar(&parsed.seed, "seed", "", "round seed; random when empty")
	flags.DurationVar(&parsed.duration, "duration", 0, "overrides the scenario duration")

	err := flags.Parse(args)
	if err != nil {
		return runFlags{}, fmt.Errorf("%w: %w", errUsage, err)
	}

	isComplete := parsed.scenario != "" && flags.NArg() == 0 && parsed.duration >= 0
	if !isComplete {
		return runFlags{}, errUsage
	}

	return parsed, nil
}

func runRound(ctx context.Context, args []string) error {
	flags, err := parseRunFlags(args)
	if err != nil {
		return err
	}

	runConfig, err := config.LoadRun(os.LookupEnv)
	if err != nil {
		return err
	}

	options, err := roundOptions(flags, runConfig)
	if err != nil {
		return err
	}

	pool, err := newPool(ctx, runConfig.AuditorURL, exportConnections)
	if err != nil {
		return err
	}
	defer pool.Close()

	options.Export = func(ctx context.Context, request export.Request) (snapshot.Snapshot, error) {
		return export.Take(ctx, pool, request)
	}
	options.Check = checkInSubprocess

	dockerClient, err := docker.New(runConfig.DockerHost, runConfig.Project)
	if err != nil {
		return err
	}

	options.Chaos = round.ChaosOptions{Docker: dockerClient, Token: runConfig.ChaosToken, EventsDir: eventsDir}

	result, err := round.Run(ctx, options)
	printSummary(result)

	if err != nil {
		return err
	}

	if !result.IsClean() {
		return errRoundIsDirty
	}

	return verdictError(result.Verdict)
}

func verdictError(verdict checker.Verdict) error {
	switch verdict {
	case checker.VerdictPass:
		return nil
	case checker.VerdictInconclusive:
		return errRoundInconclusive
	default:
		return errRoundFailed
	}
}

// checkInSubprocess runs `lab check` in its own process with its own memory
// limit, so a checker that runs out of memory never takes the lab with it.
func checkInSubprocess(ctx context.Context, roundDir string) (checker.Verdict, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("%w: %w", errCheckerCrashed, err)
	}

	command := exec.CommandContext(ctx, executable, commandCheck, "--round", roundDir) //nolint:gosec // G204: the lab runs its own binary with a directory it allocated.
	command.Env = append(os.Environ(), checkerMemoryLimit)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr

	err = command.Run()

	var exitErr *exec.ExitError

	switch {
	case err == nil:
		return checker.VerdictPass, nil
	case errors.As(err, &exitErr) && exitErr.ExitCode() == exitRoundFailed:
		return checker.VerdictFail, nil
	case errors.As(err, &exitErr) && exitErr.ExitCode() == exitRoundInconclusive:
		return checker.VerdictInconclusive, nil
	default:
		return "", fmt.Errorf("%w: %w", errCheckerCrashed, err)
	}
}

func runCheck(args []string) error {
	flags := flag.NewFlagSet(commandCheck, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	roundDir := flags.String("round", "", "round directory")

	err := flags.Parse(args)
	if err != nil || *roundDir == "" || flags.NArg() != 0 {
		return errUsage
	}

	result, err := checker.Check(*roundDir)
	if err != nil {
		return err
	}

	fmt.Printf("verdict: %s (%d violations, %d unresolved); report in %s\n", result.Verdict, len(result.Violations), len(result.Unresolved), *roundDir)

	return verdictError(result.Verdict)
}

func roundOptions(flags runFlags, runConfig config.Run) (round.Options, error) {
	scenario, err := scenarios.Load(flags.scenario)
	if err != nil {
		return round.Options{}, err
	}

	if flags.duration > 0 {
		scenario.Duration = flags.duration
	}

	roundSeed, err := seedOf(flags.seed)
	if err != nil {
		return round.Options{}, err
	}

	options := round.Options{
		Scenario:   scenario,
		Seed:       roundSeed,
		BankURL:    runConfig.BankURL,
		ControlURL: runConfig.ControlURL,
		RoundsDir:  runConfig.RoundsDir,
		LabVersion: buildinfo.Version(),
		Logger:     newLogger(),
	}

	return options, nil
}

func seedOf(raw string) (seed.Seed, error) {
	if raw == "" {
		return seed.Random()
	}

	return seed.Parse(raw)
}

func printSummary(result round.Result) {
	if result.ID == "" {
		return
	}

	report := result.History
	fmt.Printf("round %s: %d operations (ok %d, fail %d, info %d, open %d), %d records, well formed: %t, verdict: %s\n",
		result.ID, report.Invocations, report.OK, report.Fail, report.Info, len(report.Open), report.Records, report.IsWellFormed(), result.Verdict)
	fmt.Printf("archived in %s\n", result.Dir)

	for _, violation := range report.Violations {
		fmt.Printf("  index %d: %s\n", violation.Index, violation.Message)
	}
}

func newLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "lab")
}

func probeHealth(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, healthcheckURL, nil)
	if err != nil {
		return err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: got %d", errUnhealthy, response.StatusCode)
	}

	return nil
}
