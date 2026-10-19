// Command partner runs the simulated PIX partner.
//
//	partner serve        partner API (:8081) and admin and chaos port (:9092)
//	partner healthcheck  probes the local admin port; used by the container healthcheck
//	partner chaos-kill   SIGKILLs the main process of the container (chaoslab builds only)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"centavo/partner/internal/engine"
	"centavo/partner/internal/interfaces/httpapi"
	"centavo/partner/internal/modes"
	"centavo/partner/internal/platform/buildinfo"
	"centavo/partner/internal/platform/chaos"
	"centavo/partner/internal/platform/chaosbuild"
	"centavo/partner/internal/platform/chaoskill"
	"centavo/partner/internal/platform/config"
	"centavo/partner/internal/platform/httpserver"
	"centavo/partner/internal/storage"
	"centavo/partner/internal/truth"
)

const (
	commandServe       = "serve"
	commandHealthcheck = "healthcheck"
	commandChaosKill   = "chaos-kill"
)

const (
	apiAddr            = ":8081"
	adminAddr          = ":9092"
	healthcheckURL     = "http://127.0.0.1:9092" + httpapi.HealthPath
	healthcheckTimeout = 3 * time.Second
	// binaryPath is where the image installs the partner; chaos-kill looks for it.
	binaryPath = "/app/partner"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

var errUnhealthy = errors.New("health endpoint did not answer 200")

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	if len(arguments) != 1 {
		fmt.Fprintln(os.Stderr, "usage: partner serve | healthcheck | chaos-kill")

		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := runCommand(ctx, arguments[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "partner %s: %v\n", arguments[0], err)

		return exitError
	}

	return exitOK
}

func runCommand(ctx context.Context, command string) error {
	switch command {
	case commandServe:
		return serve(ctx)
	case commandChaosKill:
		return chaoskill.KillMain(binaryPath)
	case commandHealthcheck:
		return probeHealth(ctx)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func serve(ctx context.Context) error {
	configuration, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	err = config.ValidateBuild(configuration, chaosbuild.IsEnabled())
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "partner")

	if chaosbuild.IsEnabled() {
		logger.WarnContext(ctx, "chaoslab build: chaos code is compiled into this binary", "marker", chaosbuild.Sentinel(), "version", buildinfo.Version())
	}

	store, err := storage.Open(ctx, configuration.DBPath)
	if err != nil {
		return err
	}

	defer closeStore(store, logger)

	registry := modes.NewRegistry()
	participants := make([]engine.Participant, 0, len(configuration.Participants))
	participantIDs := make([]string, 0, len(configuration.Participants))

	for _, participant := range configuration.Participants {
		participants = append(participants, engine.Participant{ID: participant.ID, WebhookURL: participant.WebhookURL})
		participantIDs = append(participantIDs, participant.ID)
	}

	engineConfig := engine.Config{
		Queue: store, Saga: truth.NewSaga(store), Modes: registry, Participants: participants,
		Secret: []byte(configuration.WebhookSecret), Logger: logger,
	}
	worker := engine.New(engineConfig)
	go worker.Run(ctx)

	apiConfig := httpapi.APIConfig{Intake: truth.NewIntake(store), Reads: store, Modes: registry, Participants: participantIDs, Logger: logger}
	api := httpapi.NewAPI(apiConfig)
	buildInfo := httpapi.BuildInfo{Version: buildinfo.Version(), IsChaosBuild: chaosbuild.IsEnabled(), Env: string(configuration.Env)}
	adminHandler := httpapi.NewAdminHandler(buildInfo, api, chaos.Handler(configuration.ChaosToken, registry))
	apiServer := httpserver.New(apiAddr, api.Handler())
	adminServer := httpserver.New(adminAddr, adminHandler)

	logger.InfoContext(ctx, "serving", "api_addr", apiAddr, "admin_addr", adminAddr, "participants", participantIDs)

	return httpserver.Run(ctx, apiServer, adminServer)
}

func closeStore(store *storage.DB, logger *slog.Logger) {
	err := store.Close()
	if err != nil {
		logger.Warn("closing the truth", "error", err)
	}
}

func probeHealth(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, healthcheckURL, http.NoBody)
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
