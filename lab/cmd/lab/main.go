// Command lab runs the chaos laboratory: load generator, chaos bot, auditor,
// checker and panel. It never imports code from bank or partner.
//
//	lab serve        panel (:8090) and continuous rounds
//	lab run          one round: lab run --scenario smoke [--seed 0x5eed] [--duration 10m]
//	lab healthcheck  probes the local panel port; used by the container healthcheck
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
	"os/signal"
	"syscall"
	"time"

	"centavo/lab/internal/dashboard"
	"centavo/lab/internal/platform/buildinfo"
	"centavo/lab/internal/platform/config"
	"centavo/lab/internal/platform/httpserver"
	"centavo/lab/internal/round"
	"centavo/lab/internal/seed"
	"centavo/lab/scenarios"
)

const (
	commandServe       = "serve"
	commandRun         = "run"
	commandHealthcheck = "healthcheck"
)

const (
	panelAddr          = ":8090"
	healthcheckURL     = "http://127.0.0.1:8090" + dashboard.HealthPath
	healthcheckTimeout = 3 * time.Second
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

const usage = "usage: lab serve | run --scenario <name> [--seed <seed>] [--duration <d>] | healthcheck"

var (
	errUnhealthy    = errors.New("health endpoint did not answer 200")
	errUsage        = errors.New(usage)
	errRoundIsDirty = errors.New("the round history is malformed or has open operations")
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
	if errors.Is(err, errUsage) {
		fmt.Fprintln(os.Stderr, usage)

		return exitUsage
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "lab %s: %v\n", args[0], err)

		return exitError
	}

	return exitOK
}

func runCommand(ctx context.Context, command string, args []string) error {
	switch command {
	case commandServe:
		return serve(ctx)
	case commandRun:
		return runRound(ctx, args)
	case commandHealthcheck:
		return probeHealth(ctx)
	default:
		return errUsage
	}
}

func serve(ctx context.Context) error {
	logger := newLogger()
	handler := dashboard.NewHandler(buildinfo.Version())
	panelServer := httpserver.New(panelAddr, handler)

	logger.InfoContext(ctx, "serving", "panel_addr", panelAddr)

	return httpserver.Run(ctx, panelServer)
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

	result, err := round.Run(ctx, options)
	printSummary(result)

	if err != nil {
		return err
	}

	if !result.IsClean() {
		return errRoundIsDirty
	}

	return nil
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
	fmt.Printf("round %s: %d operations (ok %d, fail %d, info %d, open %d), %d records, well formed: %t\n",
		result.ID, report.Invocations, report.OK, report.Fail, report.Info, len(report.Open), report.Records, report.IsWellFormed())
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
