package application

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/platform/labevent"
)

// Only the robust unit of work produces these; the HTTP layer maps them.
var (
	// ErrTransient is safe to retry: nothing was applied (serialization
	// failure, deadlock, or a connection error before the request was sent).
	ErrTransient = errors.New("transient storage failure")
	// ErrOutcomeUnknown is the ambiguous one: the connection failed during or
	// after the commit, so the write may or may not be applied.
	ErrOutcomeUnknown = errors.New("the storage did not confirm the outcome")
	ErrUnavailable    = errors.New("storage temporarily unavailable")
)

// The internal retry of a unit of work: a few quick tries, well inside the
// request deadline.
const (
	unitMaxRetries = 3
	unitBaseDelay  = 20 * time.Millisecond
	unitMaxDelay   = 200 * time.Millisecond
)

type KeyClaim struct {
	Key         idempotency.Key
	Endpoint    string
	RequestHash idempotency.RequestHash
}

// Both claims race on the primary key: the second waits for the first to
// commit, which makes the key itself the lock.
type KeyStore interface {
	ClaimKey(ctx context.Context, claim KeyClaim) (idempotency.Record, bool, error)
	CompleteKey(ctx context.Context, key idempotency.Key, operationID ledger.OperationID, response idempotency.StoredResponse) error
	VoidKey(ctx context.Context, key idempotency.Key) (idempotency.Record, bool, error)
	// The write is the point: its commit waits for the synchronous standby like
	// any other, and its WAL comes after the commit that stored the answer, so
	// once it returns that answer is on the replica too.
	TouchKey(ctx context.Context, key idempotency.Key) error
}

type Write struct {
	Key         idempotency.Key
	Endpoint    string
	RequestHash idempotency.RequestHash
	Apply       func(ctx context.Context, stores Stores, operationID ledger.OperationID) (idempotency.StoredResponse, error)
}

type KeyPolicy interface {
	Run(ctx context.Context, stores Stores, write Write) (idempotency.StoredResponse, error)
}

// KeysInTheSameCommit is the idempotency defense: the key is claimed, the
// effect applied and the key completed in the same transaction, so a crash
// before the commit erases both and a retry after it gets the stored answer.
type KeysInTheSameCommit struct{}

type RetryPolicy struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

type Writer struct {
	unitOfWork UnitOfWork
	keys       KeyPolicy
	retries    RetryPolicy
}

type WriterDependencies struct {
	UnitOfWork UnitOfWork
	Keys       KeyPolicy
	Retries    RetryPolicy
}

type IdempotencyStrategy struct {
	Keys     KeyPolicy
	Resolver Resolver
	Retries  RetryPolicy
}

func (KeysInTheSameCommit) Run(ctx context.Context, stores Stores, write Write) (idempotency.StoredResponse, error) {
	claim := KeyClaim{Key: write.Key, Endpoint: write.Endpoint, RequestHash: write.RequestHash}

	existing, isClaimed, err := stores.ClaimKey(ctx, claim)
	if err != nil {
		return idempotency.StoredResponse{}, err
	}

	if !isClaimed {
		response, err := replayDurably(ctx, stores, write, existing)
		if err == nil {
			labevent.Record(ctx, labevent.IdempotentReplay, write.Endpoint, 1)
		}

		return response, err
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

// The unit is idempotent, so redoing it after a transient or ambiguous
// failure is safe, and the key decides whether it was already applied.
func UnitRetries() RetryPolicy {
	return RetryPolicy{MaxRetries: unitMaxRetries, BaseDelay: unitBaseDelay, MaxDelay: unitMaxDelay}
}

func NoRetries() RetryPolicy {
	return RetryPolicy{}
}

func NewWriter(deps WriterDependencies) Writer {
	return Writer{unitOfWork: deps.UnitOfWork, keys: deps.Keys, retries: deps.Retries}
}

func (w Writer) Execute(ctx context.Context, write Write) (idempotency.StoredResponse, error) {
	for attempt := 0; ; attempt++ {
		response, err := w.runOnce(ctx, write)

		isRetryable := errors.Is(err, ErrTransient) || errors.Is(err, ErrOutcomeUnknown)
		shouldStopRetrying := !isRetryable || attempt >= w.retries.MaxRetries
		if shouldStopRetrying {
			return response, err
		}

		isWaiting := sleep(ctx, w.retries.delay(attempt))
		if !isWaiting {
			return response, err
		}
	}
}

func RobustIdempotency(unitOfWork UnitOfWork) IdempotencyStrategy {
	return IdempotencyStrategy{Keys: KeysInTheSameCommit{}, Resolver: NewTombstoneResolver(unitOfWork), Retries: UnitRetries()}
}

// replayDurably answers a retry with the stored answer, but only once the
// commit that stored it is as durable as a new one would be. A client that gave
// up while its commit waited for the synchronous standby cancels that wait:
// PostgreSQL keeps the commit locally, unreplicated, and a retry that only read
// it would hand out an answer, or a refusal, that a failover then loses.
func replayDurably(ctx context.Context, stores KeyStore, write Write, existing idempotency.Record) (idempotency.StoredResponse, error) {
	isSettled := existing.Status != idempotency.StatusPending
	if isSettled {
		err := stores.TouchKey(ctx, write.Key)
		if err != nil {
			return idempotency.StoredResponse{}, err
		}
	}

	return existing.Replay(write.Endpoint, write.RequestHash)
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
