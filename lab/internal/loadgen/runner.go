package loadgen

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	mathrand "math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"centavo/lab/internal/history"
	"centavo/lab/internal/seed"
	"centavo/lab/scenarios"
)

// retryPolicy is how a client times out and retries one operation.
type retryPolicy struct {
	attemptTimeout time.Duration
	maxRetries     int
	backoffBase    time.Duration
	backoffCap     time.Duration
	// deadline bounds the whole operation; past it, the operation is info.
	deadline time.Duration
}

// politePolicy is the polite client.
var politePolicy = retryPolicy{
	attemptTimeout: 2 * time.Second, maxRetries: 8, backoffBase: 100 * time.Millisecond, backoffCap: 5 * time.Second, deadline: 30 * time.Second,
}

// aggressivePolicy retries at once, up to 20 times, with the same key.
var aggressivePolicy = retryPolicy{
	attemptTimeout: 2 * time.Second, maxRetries: 20, deadline: 30 * time.Second,
}

// Result is what the load leaves for the cure: the writes whose outcome is
// unknown, to resolve, and the next free process number.
type Result struct {
	Indeterminate []IndeterminateWrite
	NextProcess   int
}

// IndeterminateWrite is a write whose outcome the client does not know, with
// the request it sent: resolve must name the request, not only its key.
type IndeterminateWrite struct {
	// Op is the operation being resolved; Key is the key it sent, its own or
	// the one a buggy client reused.
	Op   string
	Key  string
	Path string
	Body json.RawMessage
}

// Amounts the workload draws, in cents.
const (
	initialFundingCents = 100_000
	minDepositCents     = 500
	maxDepositCents     = 50_000
	minTransferCents    = 100
	maxTransferCents    = 20_000
	statementPageLimit  = 20
	percent             = 100
)

// ErrPreparationFailed means the accounts or balances a round needs could not
// be created, so the round never started.
var ErrPreparationFailed = errors.New("round preparation failed")

// Config is one load phase of a round.
type Config struct {
	Round    string
	Seed     seed.Seed
	Workload scenarios.Workload
	Duration time.Duration
	BankURL  string
	History  *history.Writer
	Logger   *slog.Logger
	// PartnerURL is the partner's admin port, where the lab asks for PIX in.
	PartnerURL string
	// ParticipantID is the bank at the partner.
	ParticipantID string
	// BeforeLoad runs once the accounts exist and before the load starts;
	// the nemesis arms its faults here, so preparation never sees chaos.
	BeforeLoad func(ctx context.Context) error
}

// weightedFunction is one slice of an operation mix.
type weightedFunction struct {
	function history.Function
	weight   int
}

// mixes are the workload profiles.
var mixes = map[scenarios.Profile][]weightedFunction{
	scenarios.ProfileLedgerOnly: {
		{function: history.FunctionTransfer, weight: 50},
		{function: history.FunctionDeposit, weight: 30},
		{function: history.FunctionReadBalance, weight: 15},
		{function: history.FunctionReadStatement, weight: 5},
	},
	scenarios.ProfileBalanced: {
		{function: history.FunctionTransfer, weight: 40},
		{function: history.FunctionDeposit, weight: 20},
		{function: history.FunctionPixOut, weight: 15},
		{function: history.FunctionPixIn, weight: 10},
		{function: history.FunctionReadBalance, weight: 10},
		{function: history.FunctionReadStatement, weight: 5},
	},
	scenarios.ProfilePaymentsHeavy: {
		{function: history.FunctionPixOut, weight: 50},
		{function: history.FunctionPixIn, weight: 20},
		{function: history.FunctionTransfer, weight: 20},
		{function: history.FunctionReadBalance, weight: 7},
		{function: history.FunctionReadStatement, weight: 3},
	},
	scenarios.ProfilePaymentsOut: {
		{function: history.FunctionPixOut, weight: 50},
		{function: history.FunctionDeposit, weight: 20},
		{function: history.FunctionTransfer, weight: 15},
		{function: history.FunctionReadBalance, weight: 10},
		{function: history.FunctionReadStatement, weight: 5},
	},
}

// Shares of the PIX out that go to a key the partner does not know, so the
// rejected path runs every round.
const (
	invalidKeyPercent = 5
	destinationKeys   = 10_000
)

// run is the state the clients of one round share.
type run struct {
	config      Config
	policy      retryPolicy
	bank        bankClient
	partner     bankClient
	nextProcess atomic.Int64
	accounts    []accountID

	mu            sync.Mutex
	indeterminate []IndeterminateWrite
}

// Run prepares the accounts, then runs the workload for the configured
// duration. Operations in flight when the time is up finish normally.
func Run(ctx context.Context, config Config) (Result, error) {
	return runWith(ctx, config, politePolicy)
}

func runWith(ctx context.Context, config Config, policy retryPolicy) (Result, error) {
	r := &run{
		config: config, policy: policy, bank: newBankClient(config.BankURL, config.Workload.Clients),
		partner: newBankClient(config.PartnerURL, config.Workload.Clients), accounts: make([]accountID, config.Workload.Accounts),
	}
	r.nextProcess.Store(int64(config.Workload.Clients))

	clients := make([]*client, config.Workload.Clients)
	for index := range clients {
		clients[index] = r.newClient(index)
	}

	err := forEachClient(ctx, clients, (*client).prepare)
	if err != nil {
		return r.result(), err
	}

	if config.BeforeLoad != nil {
		err = config.BeforeLoad(ctx)
		if err != nil {
			return r.result(), err
		}
	}

	loadCtx, stopLoad := context.WithTimeout(ctx, config.Duration)
	defer stopLoad()

	tokens := make(chan struct{})
	go emitTokens(loadCtx, config.Workload.RatePerSecond, tokens)

	runLoad := func(c *client, ctx context.Context) error {
		return c.load(ctx, loadCtx, tokens)
	}

	err = forEachClient(ctx, clients, runLoad)

	return r.result(), err
}

func (r *run) result() Result {
	r.mu.Lock()
	defer r.mu.Unlock()

	return Result{Indeterminate: slices.Clone(r.indeterminate), NextProcess: int(r.nextProcess.Load()) + 1}
}

// noteIndeterminate keeps a write whose outcome the client does not know.
func (r *run) noteIndeterminate(write IndeterminateWrite) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.indeterminate = append(r.indeterminate, write)
}

// forEachClient runs step on every client concurrently and returns the first
// error, canceling the others.
func forEachClient(ctx context.Context, clients []*client, step func(*client, context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)

	for _, c := range clients {
		wg.Go(func() {
			err := step(c, ctx)
			if err == nil {
				return
			}

			once.Do(func() {
				firstErr = err
				cancel()
			})
		})
	}

	wg.Wait()

	return firstErr
}

// emitTokens paces the whole workload. A tick nobody is waiting for is
// dropped: the rate is a ceiling, and a slow bank lowers the real throughput.
func emitTokens(ctx context.Context, ratePerSecond float64, tokens chan<- struct{}) {
	ticker := time.NewTicker(time.Duration(float64(time.Second) / ratePerSecond))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		select {
		case tokens <- struct{}{}:
		default:
		}
	}
}

// client is one logical client: a goroutine that runs one operation at a time.
// Its process number changes when an operation ends as info.
type client struct {
	run     *run
	index   int
	process int
	seq     int
	profile scenarios.ClientProfile
	policy  retryPolicy
	// lastWrite is the key a buggy client reuses with another body.
	lastWrite string
	ops       *mathrand.Rand
	amounts   *mathrand.Rand
	picks     *mathrand.Rand
	jitter    *mathrand.Rand
	accounts  []int
}

func (r *run) newClient(index int) *client {
	stream := func(name string) *mathrand.Rand {
		return r.config.Seed.Stream(fmt.Sprintf("%s/%d", name, index))
	}

	profile := pickProfile(stream("workload.profiles"), r.config.Workload.ClientMix)

	return &client{
		run: r, index: index, process: index + 1, profile: profile, policy: r.policyOf(profile),
		ops: stream("workload.ops"), amounts: stream("workload.amounts"), picks: stream("workload.accounts"),
		jitter: stream("workload.clients"), accounts: r.ownedAccounts(index),
	}
}

// targetOf is who serves the operation: the partner for a PIX in, the bank otherwise.
func (r *run) targetOf(op operation) bankClient {
	if op.isToPartner {
		return r.partner
	}

	return r.bank
}

// policyOf is how a profile retries; the polite policy can be replaced in tests.
func (r *run) policyOf(profile scenarios.ClientProfile) retryPolicy {
	if profile == scenarios.ClientAggressive {
		return aggressivePolicy
	}

	return r.policy
}

// ownedAccounts deals accounts round-robin: client i prepares accounts i,
// i+clients, i+2*clients...
func (r *run) ownedAccounts(index int) []int {
	var owned []int

	for account := index; account < r.config.Workload.Accounts; account += r.config.Workload.Clients {
		owned = append(owned, account)
	}

	return owned
}

// prepare opens and funds the client's accounts (the preparation phase). A
// round cannot start without them.
func (c *client) prepare(ctx context.Context) error {
	for _, account := range c.accounts {
		holderName := fmt.Sprintf("%s client %d account %d", c.run.config.Round, c.index+1, account)

		created, err := mustSucceed(ctx, c, createAccountOperation, createAccountArgs{HolderName: holderName})
		if err != nil {
			return err
		}

		var result createAccountResult

		err = json.Unmarshal(created, &result)
		if err != nil {
			return fmt.Errorf("%w: reading the new account id: %w", ErrPreparationFailed, err)
		}

		c.run.accounts[account] = result.AccountID

		_, err = mustSucceed(ctx, c, depositOperation, depositArgs{AccountID: result.AccountID, AmountCents: initialFundingCents})
		if err != nil {
			return err
		}
	}

	return nil
}

func mustSucceed[Args any](ctx context.Context, c *client, build func(Args) (operation, error), args Args) (json.RawMessage, error) {
	op, err := build(args)
	if err != nil {
		return nil, err
	}

	result, err := c.execute(ctx, op)
	if err != nil {
		return nil, err
	}

	if result.verdict != verdictSucceeded {
		return nil, fmt.Errorf("%w: %s ended as %s", ErrPreparationFailed, op.function, result.outcome.Kind)
	}

	return result.value, nil
}

// load runs operations until loadCtx ends; ctx bounds the operations themselves.
func (c *client) load(ctx, loadCtx context.Context, tokens <-chan struct{}) error {
	for {
		select {
		case <-loadCtx.Done():
			return nil
		case <-tokens:
		}

		op, err := c.nextOperation()
		if err != nil {
			return err
		}

		err = c.perform(ctx, op)
		if err != nil {
			return err
		}
	}
}

func (c *client) nextOperation() (operation, error) {
	function := pickFunction(c.ops, mixes[c.run.config.Workload.Profile])
	accounts := c.workingAccounts()

	// A mix draws only client operations; anything else is an error below.
	//exhaustive:ignore
	switch function {
	case history.FunctionPixOut:
		amount := drawCents(c.amounts, minTransferCents, maxTransferCents)

		return pixOutOperation(pixOutArgs{AccountID: pickOne(c.picks, accounts), AmountCents: amount, DestinationKey: drawDestinationKey(c.picks)})
	case history.FunctionPixIn:
		amount := drawCents(c.amounts, minDepositCents, maxDepositCents)
		args := pixInArgs{AccountID: pickOne(c.picks, accounts), AmountCents: amount, EndToEndID: incomingEndToEndID(c.run.config.Round, c.process, c.seq+1)}

		return pixInOperation(args, c.run.config.ParticipantID)
	case history.FunctionTransfer:
		from, to := pickPair(c.picks, accounts)
		amount := drawCents(c.amounts, minTransferCents, maxTransferCents)

		return transferOperation(transferArgs{From: from, To: to, AmountCents: amount})
	case history.FunctionDeposit:
		amount := drawCents(c.amounts, minDepositCents, maxDepositCents)

		return depositOperation(depositArgs{AccountID: pickOne(c.picks, accounts), AmountCents: amount})
	case history.FunctionReadBalance:
		return readBalanceOperation(readBalanceArgs{AccountID: pickOne(c.picks, accounts)})
	case history.FunctionReadStatement:
		return readStatementOperation(readStatementArgs{AccountID: pickOne(c.picks, accounts), Limit: statementPageLimit})
	default:
		return operation{}, fmt.Errorf("the ledger-only mix has no operation %q", function)
	}
}

// workingAccounts are the accounts this client operates on during the load.
func (c *client) workingAccounts() []accountID {
	if !c.run.config.Workload.ExclusiveAccounts {
		return c.run.accounts
	}

	owned := make([]accountID, 0, len(c.accounts))
	for _, account := range c.accounts {
		owned = append(owned, c.run.accounts[account])
	}

	return owned
}

// perform runs a load operation the way the client's profile behaves.
func (c *client) perform(ctx context.Context, op operation) error {
	if c.profile == scenarios.ClientDuplicator {
		return c.executeTwice(ctx, op)
	}

	isBuggyReuse := c.profile == scenarios.ClientBuggy && op.isWrite() && c.lastWrite != ""
	if isBuggyReuse {
		reused, err := op.reusingKey(c.lastWrite)
		if err != nil {
			return err
		}

		op = reused
	}

	_, err := c.execute(ctx, op)

	return err
}

// invoke assigns the operation id, durably records the invoke and returns the
// request to send.
func (c *client) invoke(op operation) (string, request, error) {
	c.seq++
	opID := fmt.Sprintf("%s-p%02d-%06d", c.run.config.Round, c.process, c.seq)

	req := op.request
	req.correlationID = opID

	if op.isWrite() {
		req.key = cmp.Or(op.reusedKey, opID)
		c.lastWrite = ""
	}

	// Only a write that owns its key can lend it: a buggy client alternates
	// between its own key and the previous one.
	isOwnKey := op.isWrite() && op.reusedKey == ""
	if isOwnKey {
		c.lastWrite = opID
	}

	invoke := history.Record{Process: history.Client(c.process), Type: history.TypeInvoke, F: op.function, Op: opID, Value: op.invokeValue}

	_, err := c.run.config.History.AppendDurable(invoke)

	return opID, req, err
}

// execute records the invoke, retries like a polite client and records how
// the operation ended. It only returns an error when the history itself fails.
func (c *client) execute(ctx context.Context, op operation) (attemptResult, error) {
	opID, req, err := c.invoke(op)
	if err != nil {
		return attemptResult{}, err
	}

	writer := c.run.config.History
	policy := c.policy

	opCtx, cancel := context.WithTimeout(ctx, policy.deadline)
	defer cancel()

	for attempt := 1; ; attempt++ {
		result := c.run.targetOf(op).attempt(opCtx, req, policy.attemptTimeout, op.parse)

		shouldRetry := result.verdict == verdictRetry && attempt <= policy.maxRetries
		if !shouldRetry {
			return result, c.complete(op, opID, attempt, result)
		}

		record := history.Record{Process: history.Client(c.process), Type: history.TypeAttempt, Op: opID, N: attempt, Outcome: mustMarshal(result.outcome)}

		_, err := writer.Append(record)
		if err != nil {
			return attemptResult{}, err
		}

		isWaiting := sleep(opCtx, policy.backoff(c.jitter, attempt))
		if !isWaiting {
			return result, c.complete(op, opID, attempt, result)
		}
	}
}

// duplicateCopies is how many times a duplicator sends each operation.
const duplicateCopies = 2

// executeTwice is the duplicator client: the same request, with the same key,
// twice in parallel. One success is a success; two definitive failures are a
// failure; anything else is info.
func (c *client) executeTwice(ctx context.Context, op operation) error {
	opID, req, err := c.invoke(op)
	if err != nil {
		return err
	}

	opCtx, cancel := context.WithTimeout(ctx, c.policy.deadline)
	defer cancel()

	var (
		wg      sync.WaitGroup
		results [duplicateCopies]attemptResult
	)

	for i := range results {
		wg.Go(func() {
			results[i] = c.run.targetOf(op).attempt(opCtx, req, c.policy.attemptTimeout, op.parse)
		})
	}

	wg.Wait()

	for i, result := range results {
		if result.verdict == verdictSucceeded {
			continue
		}

		record := history.Record{Process: history.Client(c.process), Type: history.TypeAttempt, Op: opID, N: i + 1, Outcome: mustMarshal(result.outcome)}

		_, err := c.run.config.History.Append(record)
		if err != nil {
			return err
		}
	}

	return c.complete(op, opID, duplicateCopies, combineCopies(results[:]))
}

func combineCopies(results []attemptResult) attemptResult {
	for _, result := range results {
		if result.verdict == verdictSucceeded {
			return result
		}
	}

	for _, result := range results {
		if result.verdict != verdictFailed {
			return result
		}
	}

	return results[0]
}

// pickProfile draws the client's behavior from the scenario's weighted mix.
func pickProfile(stream *mathrand.Rand, mix map[scenarios.ClientProfile]float64) scenarios.ClientProfile {
	profiles := slices.Sorted(maps.Keys(mix))

	var total float64
	for _, profile := range profiles {
		total += mix[profile]
	}

	roll := stream.Float64() * total

	for _, profile := range profiles {
		if roll < mix[profile] {
			return profile
		}

		roll -= mix[profile]
	}

	return profiles[len(profiles)-1]
}

// complete writes the ok, fail or info record; an info retires the process.
func (c *client) complete(op operation, opID string, attempts int, result attemptResult) error {
	record := history.Record{Process: history.Client(c.process), F: op.function, Op: opID, Attempts: attempts}

	switch result.verdict {
	case verdictSucceeded:
		record.Type = history.TypeOK
		record.Value = result.value
	case verdictFailed:
		record.Type = history.TypeFail
		record.Error = mustMarshal(result.outcome)
	case verdictRetry, verdictGiveUp:
		record.Type = history.TypeInfo
		record.Error = mustMarshal(result.outcome)
	}

	_, err := c.run.config.History.Append(record)
	if err != nil {
		return err
	}

	if result.verdict == verdictGiveUp {
		c.run.config.Logger.Warn("unexpected answer from the bank", "op", opID, "http_status", result.outcome.HTTPStatus, "code", result.outcome.Code)
	}

	isIndeterminate := record.Type == history.TypeInfo
	if !isIndeterminate {
		return nil
	}

	// Resolve names the request as well as the key, so a buggy write that
	// reused a key is resolved for itself, not for the key's owner.
	if op.isWrite() {
		write := IndeterminateWrite{Op: opID, Key: cmp.Or(op.reusedKey, opID), Path: op.request.path, Body: op.request.body}
		c.run.noteIndeterminate(write)
	}

	c.retire()

	return nil
}

// retire gives the client a new process number: after an info the old
// operation may still be in flight, so the old process is no longer sequential.
func (c *client) retire() {
	c.process = int(c.run.nextProcess.Add(1))
	c.seq = 0
}

// backoff is exponential with full jitter: anywhere between zero and the cap.
func (p retryPolicy) backoff(jitter *mathrand.Rand, attempt int) time.Duration {
	ceiling := min(p.backoffBase<<(attempt-1), p.backoffCap)

	return time.Duration(jitter.Int64N(int64(ceiling) + 1))
}

// sleep waits for d, or reports false if ctx ends first.
func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func pickFunction(stream *mathrand.Rand, mix []weightedFunction) history.Function {
	roll := stream.IntN(percent)

	for _, slice := range mix {
		if roll < slice.weight {
			return slice.function
		}

		roll -= slice.weight
	}

	return mix[len(mix)-1].function
}

func pickOne(stream *mathrand.Rand, accounts []accountID) accountID {
	return accounts[stream.IntN(len(accounts))]
}

// pickPair draws two distinct accounts.
func pickPair(stream *mathrand.Rand, accounts []accountID) (accountID, accountID) {
	from := stream.IntN(len(accounts))
	to := (from + 1 + stream.IntN(len(accounts)-1)) % len(accounts)

	return accounts[from], accounts[to]
}

func drawCents(stream *mathrand.Rand, low, high int64) int64 {
	return low + stream.Int64N(high-low+1)
}

func drawDestinationKey(stream *mathrand.Rand) string {
	isInvalid := stream.IntN(percent) < invalidKeyPercent
	if isInvalid {
		return fmt.Sprintf("invalid-%04d", stream.IntN(destinationKeys))
	}

	return fmt.Sprintf("dest-%04d", stream.IntN(destinationKeys))
}

// incomingEndToEndID is the id the lab gives a PIX in: E plus 32 hex digits
// derived from the operation, so the checker can find it again.
func incomingEndToEndID(round string, process, seq int) string {
	digest := sha256.Sum256(fmt.Appendf(nil, "%s-p%02d-%06d", round, process, seq))

	return "E" + hex.EncodeToString(digest[:16])
}

// mustMarshal encodes the lab's own record types, which always encode.
func mustMarshal(value outcome) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	return encoded
}
