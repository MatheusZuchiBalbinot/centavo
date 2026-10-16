// Command lab runs the chaos laboratory: load generator, chaos bot, auditor,
// checker and panel. It never imports code from bank or partner.
//
//	lab serve        panel (:8090) and continuous rounds
//	lab healthcheck  probes the local panel port; used by the container healthcheck
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

	"centavo/lab/internal/dashboard"
	"centavo/lab/internal/platform/buildinfo"
	"centavo/lab/internal/platform/httpserver"
)

const (
	commandServe       = "serve"
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

var errUnhealthy = errors.New("health endpoint did not answer 200")

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: lab serve | healthcheck")

		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := runCommand(ctx, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "lab %s: %v\n", args[0], err)

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
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "lab")
	handler := dashboard.NewHandler(buildinfo.Version())
	panelServer := httpserver.New(panelAddr, handler)

	logger.InfoContext(ctx, "serving", "panel_addr", panelAddr)

	return httpserver.Run(ctx, panelServer)
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
