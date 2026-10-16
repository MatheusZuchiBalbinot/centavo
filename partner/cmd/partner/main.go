// Command partner runs the simulated PIX partner.
//
//	partner serve        partner API (:8081) and admin and chaos port (:9092)
//	partner healthcheck  probes the local admin port; used by the container healthcheck
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

	"centavo/partner/internal/interfaces/httpapi"
	"centavo/partner/internal/platform/buildinfo"
	"centavo/partner/internal/platform/chaosbuild"
	"centavo/partner/internal/platform/config"
	"centavo/partner/internal/platform/httpserver"
)

const (
	commandServe       = "serve"
	commandHealthcheck = "healthcheck"
)

const (
	apiAddr            = ":8081"
	adminAddr          = ":9092"
	healthcheckURL     = "http://127.0.0.1:9092" + httpapi.HealthPath
	healthcheckTimeout = 3 * time.Second
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

func run(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: partner serve | healthcheck")

		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := runCommand(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "partner %s: %v\n", args[0], err)

		return exitError
	}

	return exitOK
}

func runCommand(ctx context.Context, command string) error {
	switch command {
	case commandServe:
		return serve(ctx)
	case commandHealthcheck:
		return probeHealth(ctx)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func serve(ctx context.Context) error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	err = config.ValidateBuild(cfg, chaosbuild.IsEnabled())
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "partner")

	if chaosbuild.IsEnabled() {
		logger.WarnContext(ctx, "chaoslab build: chaos code is compiled into this binary", "marker", chaosbuild.Sentinel(), "version", buildinfo.Version())
	}

	buildInfo := httpapi.BuildInfo{Version: buildinfo.Version(), ChaosBuild: chaosbuild.IsEnabled(), Env: string(cfg.Env)}
	adminHandler := httpapi.NewAdminHandler(buildInfo)
	apiServer := httpserver.New(apiAddr, http.NewServeMux())
	adminServer := httpserver.New(adminAddr, adminHandler)

	logger.InfoContext(ctx, "serving", "api_addr", apiAddr, "admin_addr", adminAddr)

	return httpserver.Run(ctx, apiServer, adminServer)
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
