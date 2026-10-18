// Command bank runs the bank in one of its process roles.
//
//	bank api          HTTP API (:8080) and admin port (:9090)
//	bank worker       background loops and admin port (:9090)
//	bank migrate      applies the base migrations and exits
//	bank healthcheck  probes the local admin port; used by the container healthcheck
//	bank chaos-kill   SIGKILLs the main process of the container (chaoslab builds only)
package main

import (
	"cmp"
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
	"centavo/bank/internal/infrastructure/partnerclient"
	"centavo/bank/internal/infrastructure/postgres"
	"centavo/bank/internal/interfaces/httpapi"
	"centavo/bank/internal/interfaces/workers"
	"centavo/bank/internal/platform/buildinfo"
	"centavo/bank/internal/platform/chaos"
	"centavo/bank/internal/platform/chaosbuild"
	"centavo/bank/internal/platform/config"
	"centavo/bank/internal/platform/defenses"
	"centavo/bank/internal/platform/httpserver"

	// Registers the naive variants; the package is empty outside chaoslab builds.
	"centavo/bank/migrations"
)

const (
	roleAPI         role = "api"
	roleWorker      role = "worker"
	roleMigrate     role = "migrate"
	roleHealthcheck role = "healthcheck"
	roleChaosKill   role = "chaos-kill"
)

const (
	apiAddr            = ":8080"
	adminAddr          = ":9090"
	healthcheckURL     = "http://127.0.0.1:9090" + httpapi.HealthPath
	healthcheckTimeout = 3 * time.Second
	apiMaxConnections  = 10
	// binaryPath is where the images install the bank; chaos-kill looks for it.
	binaryPath           = "/app/bank"
	workerMaxConnections = 5
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

var errUnhealthy = errors.New("health endpoint did not answer 200")

type role string

// naiveVariants are the naive strategies of a chaoslab build; the production
// build has none (naive_production.go).
type naiveVariants struct {
	unitOfWork  application.UnitOfWork
	locker      application.Locker
	idempotency application.IdempotencyStrategy
}

// partner is what the worker calls on the partner: sends and status queries.
type partner interface {
	application.PartnerSubmitter
	application.PartnerStatusReader
	application.PartnerStatementReader
}

// sagaDependencies are shared by the worker's PIX loops.
type sagaDependencies struct {
	unitOfWork application.UnitOfWork
	machine    application.StateMachine
	partner    partner
	owner      string
	logger     *slog.Logger
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	if len(arguments) != 1 {
		fmt.Fprintln(os.Stderr, "usage: bank api | worker | migrate | healthcheck | chaos-kill")

		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	selectedRole := role(arguments[0])

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

	if selectedRole == roleChaosKill {
		return chaos.KillMain(binaryPath)
	}

	configuration, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}

	err = config.ValidateBuild(configuration, chaosbuild.IsEnabled())
	if err != nil {
		return err
	}

	logger := newLogger(selectedRole, configuration)
	announceBuild(ctx, logger)

	// healthcheck and chaos-kill returned above, before any configuration.
	//exhaustive:ignore
	switch selectedRole {
	case roleAPI:
		return serveAPI(ctx, configuration, logger)
	case roleWorker:
		return serveWorker(ctx, configuration, logger)
	case roleMigrate:
		return migrate(ctx, configuration, logger)
	default:
		return fmt.Errorf("unknown role %q", selectedRole)
	}
}

func serveAPI(ctx context.Context, configuration config.Config, logger *slog.Logger) error {
	poolConfig := postgres.PoolConfig{
		DatabaseURL: configuration.DatabaseURL, MaxConnections: apiMaxConnections,
		IsRelaxed: !configuration.Defenses.IsEnabled(defenses.DurableDB),
	}

	pool, err := postgres.NewPool(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()

	buildInfo := newBuildInfo(roleAPI, configuration)
	adminHandler := httpapi.NewAdminHandler(buildInfo, pool)
	err = checkSchema(ctx, pool, configuration, logger)
	if err != nil {
		return err
	}

	err = configuration.Partner.RequireWebhookSecret()
	if err != nil {
		return err
	}

	useCases, err := newUseCases(pool, configuration)
	if err != nil {
		return err
	}

	apiHandler := httpapi.NewAPIHandler(useCases, logger)
	apiServer := httpserver.New(apiAddr, apiHandler)
	adminServer := httpserver.New(adminAddr, adminHandler)

	servers, err := withChaos(roleAPI, configuration, apiServer, adminServer)
	if err != nil {
		return err
	}

	logger.InfoContext(ctx, "serving", "api_addr", apiAddr, "admin_addr", adminAddr)

	return httpserver.Run(ctx, servers...)
}

func newUseCases(pool *pgxpool.Pool, configuration config.Config) (httpapi.UseCases, error) {
	profile := configuration.Defenses
	store := postgres.NewLedgerStore(pool)

	moneyDependencies, err := newMoneyDependencies(pool, profile)
	if err != nil {
		return httpapi.UseCases{}, err
	}

	idempotencyStrategy, err := newIdempotency(pool, profile, moneyDependencies.UnitOfWork)
	if err != nil {
		return httpapi.UseCases{}, err
	}

	publication := newPublication(pool, profile)
	writerDependencies := application.WriterDependencies{
		UnitOfWork: moneyDependencies.UnitOfWork, Keys: idempotencyStrategy.Keys, Retries: idempotencyStrategy.Retries,
	}

	useCases := httpapi.UseCases{
		Writer: application.NewWriter(writerDependencies), Resolver: idempotencyStrategy.Resolver,
		OpenAccount: application.NewOpenAccount(store), GetAccount: application.NewGetAccount(store),
		GetStatement: application.NewGetStatement(store, store), Deposit: application.NewDeposit(moneyDependencies),
		Transfer: application.NewTransferInternal(moneyDependencies),
		Pix: httpapi.PixUseCases{
			Create: application.NewCreatePixOut(moneyDependencies.Locker, publication), Publisher: publication,
			Get: application.NewGetExternalTransfer(store), Webhook: newWebhook(profile, moneyDependencies.UnitOfWork, newStateMachine(profile)),
			WebhookSecret: []byte(configuration.Partner.WebhookSecret),
		},
	}

	return useCases, nil
}

// newPublication picks outbox: the outbox row in the transfer's own commit, or
// the naive NOTIFY after it.
func newPublication(pool *pgxpool.Pool, profile defenses.Profile) application.Publication {
	if profile.IsEnabled(defenses.Outbox) {
		return application.OutboxPublication{}
	}

	return application.NewNotifyAfterCommit(postgres.NewNotifyPublisher(pool))
}

// newWebhook picks inbox: write to the inbox, or apply in the request.
func newWebhook(profile defenses.Profile, unitOfWork application.UnitOfWork, machine application.StateMachine) httpapi.WebhookApplier {
	if profile.IsEnabled(defenses.Inbox) {
		return application.NewInboxIntake(unitOfWork)
	}

	return application.NewNaiveWebhook(unitOfWork, machine)
}

// newStateMachine picks state_machine: guarded transitions, or the naive
// overwrite.
func newStateMachine(profile defenses.Profile) application.StateMachine {
	if profile.IsEnabled(defenses.StateMachine) {
		return application.GuardedTransitions{}
	}

	return application.OverwriteTransitions{}
}

// newIdempotency picks idempotency. Its key shares the commit of the effect, so
// it needs atomic_ledger: without atomic_ledger the naive variant runs
// (Profile.Notes says so).
func newIdempotency(pool *pgxpool.Pool, profile defenses.Profile, unitOfWork application.UnitOfWork) (application.IdempotencyStrategy, error) {
	isRobust := profile.IsEnabled(defenses.Idempotency) && profile.IsEnabled(defenses.AtomicLedger)
	if isRobust {
		return application.RobustIdempotency(unitOfWork), nil
	}

	naive, err := newNaiveVariants(pool)
	if err != nil {
		return application.IdempotencyStrategy{}, fmt.Errorf("%w: %s", err, defenses.Idempotency)
	}

	return naive.idempotency, nil
}

// newMoneyDependencies is the composition root: the robust strategy unless the
// profile turns its defense off, in which case the naive variant of the
// chaoslab build.
func newMoneyDependencies(pool *pgxpool.Pool, profile defenses.Profile) (application.MoneyDependencies, error) {
	deps := application.MoneyDependencies{UnitOfWork: postgres.NewUnitOfWork(pool), Locker: application.RowLocker{}}

	isAllRobust := profile.IsEnabled(defenses.AtomicLedger) && profile.IsEnabled(defenses.BalanceLocking)
	if isAllRobust {
		return deps, nil
	}

	naive, err := newNaiveVariants(pool)
	if err != nil {
		return application.MoneyDependencies{}, fmt.Errorf("%w: profile %s", err, profile)
	}

	if !profile.IsEnabled(defenses.AtomicLedger) {
		deps.UnitOfWork = naive.unitOfWork
	}

	if !profile.IsEnabled(defenses.BalanceLocking) {
		deps.Locker = naive.locker
	}

	return deps, nil
}

func serveWorker(ctx context.Context, configuration config.Config, logger *slog.Logger) error {
	poolConfig := postgres.PoolConfig{
		DatabaseURL: configuration.DatabaseURL, MaxConnections: workerMaxConnections,
		IsRelaxed: !configuration.Defenses.IsEnabled(defenses.DurableDB),
	}

	pool, err := postgres.NewPool(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()

	err = checkSchema(ctx, pool, configuration, logger)
	if err != nil {
		return err
	}

	buildInfo := newBuildInfo(roleWorker, configuration)
	adminHandler := httpapi.NewAdminHandler(buildInfo, pool)
	adminServer := httpserver.New(adminAddr, adminHandler)

	servers, err := withChaos(roleWorker, configuration, adminServer)
	if err != nil {
		return err
	}

	if configuration.Defenses.IsEnabled(defenses.Idempotency) {
		go workers.CleanKeys(ctx, postgres.NewLedgerStore(pool), logger)
	}

	err = startSaga(ctx, pool, configuration, logger)
	if err != nil {
		return err
	}

	logger.InfoContext(ctx, "serving", "admin_addr", adminAddr)

	return httpserver.Run(ctx, servers...)
}

// startSaga starts the worker's PIX loops, each in the variant the profile
// picks: the relay (outbox, partner_idempotency, resilient_client), the inbox
// processor (inbox) and the sweeper (recovery).
func startSaga(ctx context.Context, pool *pgxpool.Pool, configuration config.Config, logger *slog.Logger) error {
	err := configuration.Partner.RequireEndpoint()
	if err != nil {
		return err
	}

	moneyDependencies, err := newMoneyDependencies(pool, configuration.Defenses)
	if err != nil {
		return err
	}

	saga := sagaDependencies{
		unitOfWork: moneyDependencies.UnitOfWork, machine: newStateMachine(configuration.Defenses), partner: newPartner(configuration),
		owner: cmp.Or(configuration.Chaos.Instance, instanceName()), logger: logger,
	}

	startRelay(ctx, pool, configuration.Defenses, saga)

	if configuration.Defenses.IsEnabled(defenses.Inbox) {
		processorConfig := application.InboxProcessorConfig{UnitOfWork: saga.unitOfWork, Machine: saga.machine, Partner: saga.partner, Logger: logger}
		go application.NewInboxProcessor(processorConfig).Run(ctx)
	}

	if configuration.Defenses.IsEnabled(defenses.Recovery) {
		sweeperConfig := application.SweeperConfig{
			UnitOfWork: saga.unitOfWork, Lease: newLeasePolicy(configuration.Defenses), Machine: saga.machine, Partner: saga.partner, Owner: saga.owner, Logger: logger,
		}
		go application.NewSweeper(sweeperConfig).Run(ctx)
	}

	reconcilerConfig := application.ReconcilerConfig{
		UnitOfWork: saga.unitOfWork, Lease: newLeasePolicy(configuration.Defenses), Machine: saga.machine,
		Statement: saga.partner, Policy: newBatchPolicy(configuration.Defenses), Owner: saga.owner, Logger: logger,
	}
	go application.NewReconciler(reconcilerConfig).Run(ctx, application.ReconcileInterval)

	return nil
}

// newLeasePolicy picks fencing: leases by the database's clock with a token
// checked at write time, or by the worker's own clock and word.
func newLeasePolicy(profile defenses.Profile) application.LeasePolicy {
	if profile.IsEnabled(defenses.Fencing) {
		return application.FencedLeases{}
	}

	return application.LocalClockLeases{}
}

// newBatchPolicy picks reconciliation: a batch in one commit with the cursor,
// or an entry at a time with the cursor saved last.
func newBatchPolicy(profile defenses.Profile) application.BatchPolicy {
	if profile.IsEnabled(defenses.Reconciliation) {
		return application.AtomicBatches{}
	}

	return application.EntryByEntry{}
}

// newPartner picks resilient_client's circuit breaker around the partner
// client.
func newPartner(configuration config.Config) partner {
	client := partnerclient.New(configuration.Partner.BaseURL, configuration.Partner.ParticipantID)

	if configuration.Defenses.IsEnabled(defenses.ResilientClient) {
		return partnerclient.NewBreaker(client)
	}

	return client
}

// startRelay starts what sends PIX out to the partner: the outbox relay with
// outbox, or the naive LISTEN relay.
func startRelay(ctx context.Context, pool *pgxpool.Pool, profile defenses.Profile, saga sagaDependencies) {
	if profile.IsEnabled(defenses.Outbox) {
		relayConfig := application.OutboxRelayConfig{
			UnitOfWork: saga.unitOfWork, Partner: saga.partner, EndToEnd: newEndToEnd(profile), Machine: saga.machine,
			Doubt: newDoubt(profile, saga.machine), Owner: saga.owner, LeaseTTL: application.OutboxLeaseTTL, Logger: saga.logger,
		}
		go application.NewOutboxRelay(relayConfig).Run(ctx)

		return
	}

	relay := application.NewNaiveRelay(saga.unitOfWork, saga.partner, saga.machine)

	listener := postgres.Listener{Pool: pool, Channel: postgres.PixOutChannel, Handle: workers.RelayNotified(relay, saga.logger), Logger: saga.logger}
	go listener.Run(ctx)
	go relayPendingOnBoot(ctx, relay, saga.logger)
}

// newEndToEnd picks partner_idempotency: one end_to_end_id per transfer, or one
// per attempt.
func newEndToEnd(profile defenses.Profile) application.EndToEndPolicy {
	if profile.IsEnabled(defenses.PartnerIdempotency) {
		return application.FixedEndToEnd{}
	}

	return application.NewEndToEndPerAttempt{}
}

// newDoubt picks resilient_client's answer to a doubtful send: retry later, or
// give up.
func newDoubt(profile defenses.Profile, machine application.StateMachine) application.DoubtPolicy {
	if profile.IsEnabled(defenses.ResilientClient) {
		return application.RetryOnDoubt{}
	}

	return application.NewGiveUpOnDoubt(machine)
}

func relayPendingOnBoot(ctx context.Context, relay application.NaiveRelay, logger *slog.Logger) {
	err := relay.RelayPending(ctx)
	if err != nil {
		logger.WarnContext(ctx, "relaying pending transfers at startup", "error", err)
	}
}

// withChaos arms the chaos controls and adds the chaos port; a production
// build gets back exactly the servers it passed in.
func withChaos(selectedRole role, configuration config.Config, servers ...*http.Server) ([]*http.Server, error) {
	chaosConfig := chaos.Config{
		Token: configuration.Chaos.Token, FailpointSpec: configuration.Chaos.FailpointSpec, Seed: configuration.Chaos.Seed,
		Service: "bank-" + string(selectedRole), Instance: cmp.Or(configuration.Chaos.Instance, instanceName()),
	}

	err := chaos.Setup(chaosConfig)
	if err != nil {
		return nil, err
	}

	chaosServer := chaos.Server(chaosConfig)
	if chaosServer == nil {
		return servers, nil
	}

	return append(servers, chaosServer), nil
}

func migrate(ctx context.Context, configuration config.Config, logger *slog.Logger) error {
	baseMigrations, err := migrations.Base()
	if err != nil {
		return fmt.Errorf("loading base migrations: %w", err)
	}

	err = postgres.MigrateBase(ctx, configuration.DatabaseURL, baseMigrations, logger)
	if err != nil {
		return err
	}

	defenseMigrations, err := defenseMigrationsOf(configuration.Defenses)
	if err != nil {
		return err
	}

	return postgres.MigrateDefenses(ctx, configuration.DatabaseURL, defenseMigrations, logger)
}

// defenseMigrationsOf lists the defenses that have constraints of their own.
func defenseMigrationsOf(profile defenses.Profile) ([]postgres.DefenseMigrations, error) {
	var list []postgres.DefenseMigrations

	for _, defense := range defenses.All() {
		files, hasMigrations, err := migrations.Defense(string(defense))
		if err != nil {
			return nil, fmt.Errorf("loading %s migrations: %w", defense, err)
		}

		if hasMigrations {
			list = append(list, postgres.DefenseMigrations{Name: string(defense), Files: files, IsEnabled: profile.IsEnabled(defense)})
		}
	}

	return list, nil
}

// checkSchema is the startup validation. A mismatch refuses to start; an
// unreachable database does not, because under chaos it comes back, and bank
// migrate already ran before.
func checkSchema(ctx context.Context, pool *pgxpool.Pool, configuration config.Config, logger *slog.Logger) error {
	err := checkDurability(ctx, pool, configuration, logger)
	if err != nil {
		return err
	}

	return checkDefenseSchema(ctx, pool, configuration, logger)
}

// checkDurability is durable_db's startup check: a robust bank refuses to run
// on connections that acknowledge commits before they are durable. As with the
// schema, an unreachable database is only a warning.
func checkDurability(ctx context.Context, pool *pgxpool.Pool, configuration config.Config, logger *slog.Logger) error {
	if !configuration.Defenses.IsEnabled(defenses.DurableDB) {
		return nil
	}

	err := postgres.CheckDurability(ctx, pool)
	if errors.Is(err, postgres.ErrNotDurable) {
		return err
	}

	if err != nil {
		logger.WarnContext(ctx, "could not check durability at startup", "error", err)
	}

	return nil
}

func checkDefenseSchema(ctx context.Context, pool *pgxpool.Pool, configuration config.Config, logger *slog.Logger) error {
	defenseMigrations, err := defenseMigrationsOf(configuration.Defenses)
	if err != nil {
		return err
	}

	err = postgres.CheckDefenseSchema(ctx, pool, defenseMigrations)
	if errors.Is(err, postgres.ErrSchemaProfileMismatch) {
		return err
	}

	if err != nil {
		logger.WarnContext(ctx, "could not check the defense schema at startup", "error", err)
	}

	return nil
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

func newBuildInfo(selectedRole role, configuration config.Config) httpapi.BuildInfo {
	return httpapi.BuildInfo{
		Service: "bank-" + string(selectedRole), Version: buildinfo.Version(), IsChaosBuild: chaosbuild.IsEnabled(),
		Env: string(configuration.Env), Defenses: configuration.Defenses.String(),
		// Notes explain combinations that weaken another defense.
		DefenseNotes: configuration.Defenses.Notes(),
	}
}

func newLogger(selectedRole role, configuration config.Config) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, nil)

	return slog.New(handler).With("service", "bank-"+string(selectedRole), "instance", instanceName(), "defenses", configuration.Defenses.String())
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
