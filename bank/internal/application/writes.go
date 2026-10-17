package application

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
)

// Errors the storage layer classifies its failures into (docs/architecture.md,
// section 7.1). The HTTP layer maps them; only the robust unit of work
// produces them.
var (
	// ErrTransient is safe to retry: nothing was applied (serialization
	// failure, deadlock, or a connection error before the request was sent).
	ErrTransient = errors.New("transient storage failure")
	// ErrOutcomeUnknown is the ambiguous one: the connection failed during or
	// after the commit, so the write may or may not be applied.
	ErrOutcomeUnknown = errors.New("the storage did not confirm the outcome")
	// ErrUnavailable means the storage gave up on the request in time.
	ErrUnavailable = errors.New("storage temporarily unavailable")
)

// KeyClaim is a request trying to own an idempotency key.
type KeyClaim struct {
	Key         idempotency.Key
	Endpoint    string
	RequestHash idempotency.RequestHash
}

// KeyStore keeps idempotency keys. Both claims race on the primary key: the
// second one waits for the first to commit, which makes the key itself the lock.
type KeyStore interface {
	// ClaimKey inserts the key as pending and reports true, or returns the
	// existing record and false.
	ClaimKey(ctx context.Context, claim KeyClaim) (idempotency.Record, bool, error)
	CompleteKey(ctx context.Context, key idempotency.Key, operationID ledger.OperationID, response idempotency.StoredResponse) error
	// VoidKey inserts a tombstone and reports true, or returns the existing
	// record and false.
	VoidKey(ctx context.Context, key idempotency.Key) (idempotency.Record, bool, error)
	// TouchKey rewrites the key's row unchanged. The write is the point: its
	// commit waits for the synchronous standby like any other, and its WAL
	// comes after the commit that stored the answer, so once it returns that
	// answer is on the replica too.
	TouchKey(ctx context.Context, key idempotency.Key) error
}

// Write is one client write, ready to run exactly once per key.
type Write struct {
	Key         idempotency.Key
	Endpoint    string
	RequestHash idempotency.RequestHash
	// Apply runs the use case in the unit of work and encodes the answer.
	Apply func(ctx context.Context, stores Stores, operationID ledger.OperationID) (idempotency.StoredResponse, error)
}

// replayDurably answers a retry with the stored answer, but only once the
// commit that stored it is as durable as a new one would be. A client that
// gave up while its commit waited for the synchronous standby cancels that
// wait: PostgreSQL keeps the commit locally, unreplicated, and a retry that
// only read it would hand out an answer, or a refusal, that a failover then
// loses (PM-016).
func replayDurably(ctx context.Context, stores Stores, write Write, existing idempotency.Record) (idempotency.StoredResponse, error) {
	isSettled := existing.Status != idempotency.StatusPending
	if isSettled {
		err := stores.TouchKey(ctx, write.Key)
		if err != nil {
			return idempotency.StoredResponse{}, err
		}
	}

	return existing.Replay(write.Endpoint, write.RequestHash)
}

// KeyPolicy is defense D3's strategy inside a unit of work.
type KeyPolicy interface {
	Run(ctx context.Context, stores Stores, write Write) (idempotency.StoredResponse, error)
}

// KeysInTheSameCommit is defense D3 (docs/domain.md, section 5): the key is
// claimed, the effect applied and the key completed in the same transaction,
// so a crash before the commit erases both and a retry after it gets the
// stored answer.
type KeysInTheSameCommit struct{}

// Run claims the key or replays it.
func (KeysInTheSameCommit) Run(ctx context.Context, stores Stores, write Write) (idempotency.StoredResponse, error) {
	claim := KeyClaim{Key: write.Key, Endpoint: write.Endpoint, RequestHash: write.RequestHash}

	existing, isClaimed, err := stores.ClaimKey(ctx, claim)
	if err != nil {
		return idempotency.StoredResponse{}, err
	}

	if !isClaimed {
		return replayDurably(ctx, stores, write, existing)
	}

	operationID, err := ledger.NewOperationID()
	if err != nil {
		return idempotency.StoredResponse{}, err
	}

	response, err := write.Apply(ctx, stores, operationID)
	if err != nil {
		return idempotency.StoredResponse{}, err
	}

	err = stores.CompleteKey(ctx, write.Key, operationID, response)
	if err != nil {
		return idempotency.StoredResponse{}, err
	}

	return response, nil
}

// RetryPolicy bounds how often a unit of work is redone.
type RetryPolicy struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// UnitRetries is D3's internal retry (docs/architecture.md, section 7): the
// unit is idempotent, so redoing it after a transient or ambiguous failure is
// safe, and the key decides whether it was already applied.
func UnitRetries() RetryPolicy {
	return RetryPolicy{MaxRetries: 3, BaseDelay: 20 * time.Millisecond, MaxDelay: 200 * time.Millisecond}
}

// NoRetries is the naive bank: whatever fails, fails.
func NoRetries() RetryPolicy {
	return RetryPolicy{}
}

// Writer runs client writes.
type Writer struct {
	unitOfWork UnitOfWork
	keys       KeyPolicy
	retries    RetryPolicy
}

// WriterDependencies are the strategies a writer runs on.
type WriterDependencies struct {
	UnitOfWork UnitOfWork
	Keys       KeyPolicy
	Retries    RetryPolicy
}

// NewWriter builds a writer.
func NewWriter(deps WriterDependencies) Writer {
	return Writer{unitOfWork: deps.UnitOfWork, keys: deps.Keys, retries: deps.Retries}
}

// Execute runs the write, redoing the whole unit on transient and ambiguous
// failures while the policy allows.
func (w Writer) Execute(ctx context.Context, write Write) (idempotency.StoredResponse, error) {
	for attempt := 0; ; attempt++ {
		response, err := w.runOnce(ctx, write)

		isRetryable := errors.Is(err, ErrTransient) || errors.Is(err, ErrOutcomeUnknown)
		if !isRetryable || attempt >= w.retries.MaxRetries {
			return response, err
		}

		isWaiting := sleep(ctx, w.retries.delay(attempt))
		if !isWaiting {
			return response, err
		}
	}
}

func (w Writer) runOnce(ctx context.Context, write Write) (idempotency.StoredResponse, error) {
	var response idempotency.StoredResponse

	err := w.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		response, err = w.keys.Run(ctx, stores, write)

		return err
	})

	return response, err
}

// delay is exponential with full jitter.
func (p RetryPolicy) delay(attempt int) time.Duration {
	ceiling := min(p.BaseDelay<<attempt, p.MaxDelay)
	if ceiling <= 0 {
		return 0
	}

	// Jitter only spreads retries out; it needs no cryptographic randomness.
	return time.Duration(rand.Int64N(int64(ceiling) + 1)) //nolint:gosec // G404: jitter, not secrecy.
}

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

// Resolution is what resolve answers about one key.
type Resolution struct {
	Outcome idempotency.Outcome
	// Response is the stored answer of an applied operation, when the bank has it.
	Response *idempotency.StoredResponse
}

// ResolveRequest is the request whose outcome the client asks about: its key
// and what it sent, since a key can end up holding another request (FC43).
type ResolveRequest struct {
	Key         idempotency.Key
	Endpoint    string
	RequestHash idempotency.RequestHash
}

// Resolver turns an operation with an unknown outcome into a known one
// (docs/domain.md, section 5.1).
type Resolver interface {
	Resolve(ctx context.Context, request ResolveRequest) (Resolution, error)
}

// TombstoneResolver is D3's resolve: a missing key gets a tombstone, so a
// late original request can never apply after the answer "voided".
type TombstoneResolver struct {
	unitOfWork UnitOfWork
}

// NewTombstoneResolver builds the resolver.
func NewTombstoneResolver(unitOfWork UnitOfWork) TombstoneResolver {
	return TombstoneResolver{unitOfWork: unitOfWork}
}

// Resolve voids an unknown key or reports how a known one ended.
func (r TombstoneResolver) Resolve(ctx context.Context, request ResolveRequest) (Resolution, error) {
	var resolution Resolution

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		resolution, err = resolveKey(ctx, stores, request)

		return err
	})

	return resolution, err
}

func resolveKey(ctx context.Context, stores Stores, request ResolveRequest) (Resolution, error) {
	existing, isVoided, err := stores.VoidKey(ctx, request.Key)
	if err != nil {
		return Resolution{}, err
	}

	if isVoided {
		return Resolution{Outcome: idempotency.OutcomeVoided}, nil
	}

	if existing.Status == idempotency.StatusPending {
		return Resolution{}, idempotency.ErrRequestInProgress
	}

	err = stores.TouchKey(ctx, request.Key)
	if err != nil {
		return Resolution{}, err
	}

	if existing.Status == idempotency.StatusVoided {
		return Resolution{Outcome: idempotency.OutcomeVoided}, nil
	}

	// The same check a retry gets: the stored answer belongs to the request
	// that took the key, and only that request may hear "applied".
	isSameRequest := existing.Endpoint == request.Endpoint && existing.RequestHash == request.RequestHash
	if !isSameRequest {
		return Resolution{Outcome: idempotency.OutcomeSuperseded}, nil
	}

	response := existing.Response

	return Resolution{Outcome: idempotency.OutcomeApplied, Response: &response}, nil
}

// IdempotencyStrategy is everything defense D3 decides.
type IdempotencyStrategy struct {
	Keys     KeyPolicy
	Resolver Resolver
	Retries  RetryPolicy
}

// RobustIdempotency is D3: keys in the same commit as the effect, resolve
// with tombstones and internal retries of the idempotent unit.
func RobustIdempotency(unitOfWork UnitOfWork) IdempotencyStrategy {
	return IdempotencyStrategy{Keys: KeysInTheSameCommit{}, Resolver: NewTombstoneResolver(unitOfWork), Retries: UnitRetries()}
}
