// Command bank runs the bank in one of its process roles.
//
//	bank api          HTTP API (:8080) and admin port (:9090)
//	bank worker       background loops and admin port (:9090)
//	bank migrate      applies the base migrations and exits
//	bank healthcheck  probes the local admin port; used by the container healthcheck
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

	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/application"
	"centavo/bank/internal/infrastructure/postgres"
	"centavo/bank/internal/interfaces/httpapi"
	"centavo/bank/internal/platform/buildinfo"
	"centavo/bank/internal/platform/chaosbuild"
	"centavo/bank/internal/platform/config"
	"centavo/bank/internal/platform/httpserver"
	"centavo/bank/migrations"
)

type role string

const (
	roleAPI         role = "api"
	roleWorker      role = "worker"
	roleMigrate     role = "migrate"
	roleHealthcheck role = "healthcheck"
)

const (
	apiAddr              = ":8080"
	adminAddr            = ":9090"
	healthcheckURL       = "http://127.0.0.1:9090" + httpapi.HealthPath
	healthcheckTimeout   = 3 * time.Second
	apiMaxConnections    = 10
	workerMaxConnections = 5
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
		fmt.Fprintln(os.Stderr, "usage: bank api | worker | migrate | healthcheck")

		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	selectedRole := role(args[0])

	err := runRole(ctx, selectedRole)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bank %s: %v\n", selectedRole, err)

		return exitError
	}

	return exitOK
}

func runRole(ctx context.Context, selectedRole role) error {
	if selectedRole == roleHealthcheck {
		return probeHealth(ctx)
	}

	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	err = config.ValidateBuild(cfg, chaosbuild.IsEnabled())
	if err != nil {
		return err
	}

	logger := newLogger(selectedRole, cfg)
	announceBuild(ctx, logger)

	switch selectedRole {
	case roleAPI:
		return serveAPI(ctx, cfg, logger)
	case roleWorker:
		return serveWorker(ctx, cfg, logger)
	case roleMigrate:
		return migrate(ctx, cfg, logger)
	default:
		return fmt.Errorf("unknown role %q", selectedRole)
	}
}

func serveAPI(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	poolConfig := postgres.PoolConfig{DatabaseURL: cfg.DatabaseURL, MaxConnections: apiMaxConnections}

	pool, err := postgres.NewPool(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()

	buildInfo := newBuildInfo(roleAPI, cfg)
	adminHandler := httpapi.NewAdminHandler(buildInfo, pool)
	apiHandler := httpapi.NewAPIHandler(newUseCases(pool), logger)
	apiServer := httpserver.New(apiAddr, apiHandler)
	adminServer := httpserver.New(adminAddr, adminHandler)

	logger.InfoContext(ctx, "serving", "api_addr", apiAddr, "admin_addr", adminAddr)

	return httpserver.Run(ctx, apiServer, adminServer)
}

func newUseCases(pool *pgxpool.Pool) httpapi.UseCases {
	store := postgres.NewLedgerStore(pool)
	moneyDependencies := application.MoneyDependencies{Positions: store, Ledger: store}

	return httpapi.UseCases{
		OpenAccount:  application.NewOpenAccount(store),
		GetAccount:   application.NewGetAccount(store),
		GetStatement: application.NewGetStatement(store, store),
		Deposit:      application.NewDeposit(moneyDependencies),
		Transfer:     application.NewTransferInternal(moneyDependencies),
	}
}

func serveWorker(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	poolConfig := postgres.PoolConfig{DatabaseURL: cfg.DatabaseURL, MaxConnections: workerMaxConnections}

	pool, err := postgres.NewPool(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()

	buildInfo := newBuildInfo(roleWorker, cfg)
	adminHandler := httpapi.NewAdminHandler(buildInfo, pool)
	adminServer := httpserver.New(adminAddr, adminHandler)

	logger.InfoContext(ctx, "serving", "admin_addr", adminAddr)

	return httpserver.Run(ctx, adminServer)
}

func migrate(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	baseMigrations, err := migrations.Base()
	if err != nil {
		return fmt.Errorf("loading base migrations: %w", err)
	}

	return postgres.MigrateBase(ctx, cfg.DatabaseURL, baseMigrations, logger)
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

func newBuildInfo(selectedRole role, cfg config.Config) httpapi.BuildInfo {
	return httpapi.BuildInfo{
		Service:    "bank-" + string(selectedRole),
		Version:    buildinfo.Version(),
		ChaosBuild: chaosbuild.IsEnabled(),
		Env:        string(cfg.Env),
		Defenses:   cfg.Defenses.String(),
	}
}

func newLogger(selectedRole role, cfg config.Config) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, nil)

	return slog.New(handler).With("service", "bank-"+string(selectedRole), "instance", instanceName(), "defenses", cfg.Defenses.String())
}

// instanceName is the container hostname, which tells instances apart in the logs.
func instanceName() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}

	return hostname
}

func announceBuild(ctx context.Context, logger *slog.Logger) {
	if !chaosbuild.IsEnabled() {
		return
	}

	logger.WarnContext(ctx, "chaoslab build: chaos code is compiled into this binary", "marker", chaosbuild.Sentinel(), "version", buildinfo.Version())
}
