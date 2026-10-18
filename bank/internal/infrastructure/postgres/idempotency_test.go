package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/infrastructure/postgres"
)

const depositEndpoint = "/v1/deposits"

// writes is the robust idempotency stack over the test database.
type writes struct {
	bank     bank
	writer   application.Writer
	resolver application.Resolver
}

func newWrites(t *testing.T) writes {
	t.Helper()

	b := newBank(t)
	unitOfWork := postgres.NewUnitOfWork(b.pool)
	strategy := application.RobustIdempotency(unitOfWork)
	writer := application.NewWriter(application.WriterDependencies{UnitOfWork: unitOfWork, Keys: strategy.Keys, Retries: strategy.Retries})

	return writes{bank: b, writer: writer, resolver: strategy.Resolver}
}

// depositWrite builds the write the HTTP layer would: hash of the raw body,
// answer encoded in the unit of work.
func (w writes) depositWrite(t *testing.T, key idempotency.Key, account ledger.AccountID, cents money.Cents) application.Write {
	t.Helper()

	body := []byte(fmt.Sprintf(`{"account_id": %q, "amount_cents": %d}`, account, cents))
	input := application.DepositInput{AccountID: account, Amount: amountOf(t, cents), Write: application.WriteContext{Key: key, CorrelationID: string(key)}}
	deposit := w.bank.deposit

	apply := func(ctx context.Context, stores application.Stores, operationID ledger.OperationID) (idempotency.StoredResponse, error) {
		result, err := deposit.Apply(ctx, stores, operationID, input)
		if err != nil {
			return idempotency.StoredResponse{}, err
		}

		encoded, err := json.Marshal(map[string]any{"transaction_id": result.TransactionID.String(), "balance_cents": result.Balance.Cents()})

		return idempotency.StoredResponse{Status: 201, Body: encoded}, err
	}

	return application.Write{Key: key, Endpoint: depositEndpoint, RequestHash: idempotency.HashRequest(depositEndpoint, body), Apply: apply}
}

func newKey() idempotency.Key {
	return idempotency.Key("test-" + uuid.NewString())
}

// TestWrite_RetryAfterCrashPostCommit_ReturnsStoredResponse is a regression
// test: the retry after a lost answer gets the stored answer, byte for byte,
// and the ledger has one application.
func TestWrite_RetryAfterCrashPostCommit_ReturnsStoredResponse(t *testing.T) {
	w := newWrites(t)
	account := w.bank.newCustomer(t)
	key := newKey()

	first, err := w.writer.Execute(t.Context(), w.depositWrite(t, key, account, 700))
	if err != nil {
		t.Fatal(err)
	}

	retry, err := w.writer.Execute(t.Context(), w.depositWrite(t, key, account, 700))
	if err != nil {
		t.Fatal(err)
	}

	isSameAnswer := first.Status == retry.Status && bytes.Equal(first.Body, retry.Body)
	if !isSameAnswer {
		t.Fatalf("first %s, retry %s", first.Body, retry.Body)
	}

	if got := w.bank.transactionsWithClientRef(t, key); got != 1 {
		t.Fatalf("the retry applied again: %d transactions", got)
	}
}

func TestWrite_SameKeyWithAnotherBodyIsRefused(t *testing.T) {
	w := newWrites(t)
	account := w.bank.newCustomer(t)
	key := newKey()

	_, err := w.writer.Execute(t.Context(), w.depositWrite(t, key, account, 700))
	if err != nil {
		t.Fatal(err)
	}

	_, err = w.writer.Execute(t.Context(), w.depositWrite(t, key, account, 701))
	if !errors.Is(err, idempotency.ErrKeyMismatch) {
		t.Fatalf("got %v, want ErrKeyMismatch", err)
	}
}

// TestWrite_ConcurrentRequestsWithTheSameKey_ApplyOnce is the duplicator client
// against idempotency: the primary key serializes the copies.
func TestWrite_ConcurrentRequestsWithTheSameKey_ApplyOnce(t *testing.T) {
	w := newWrites(t)
	account := w.bank.newCustomer(t)
	key := newKey()

	const copies = 8

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		responses [][]byte
	)

	for range copies {
		wg.Go(func() {
			response, err := w.writer.Execute(context.Background(), w.depositWrite(t, key, account, 300))

			mu.Lock()
			defer mu.Unlock()

			if err != nil {
				t.Errorf("copy failed: %v", err)

				return
			}

			responses = append(responses, response.Body)
		})
	}

	wg.Wait()

	for _, response := range responses {
		if !bytes.Equal(response, responses[0]) {
			t.Fatalf("copies got different answers: %s and %s", response, responses[0])
		}
	}

	if got := w.bank.transactionsWithClientRef(t, key); got != 1 {
		t.Fatalf("%d copies applied %d times", copies, got)
	}
}

func TestResolve_VoidsUnknownKeysForGood(t *testing.T) {
	w := newWrites(t)
	account := w.bank.newCustomer(t)
	unknown, applied := newKey(), newKey()

	resolution, err := w.resolver.Resolve(t.Context(), resolveOf(w.depositWrite(t, unknown, account, 100)))
	if err != nil || resolution.Outcome != idempotency.OutcomeVoided {
		t.Fatalf("unknown key: %+v, %v", resolution, err)
	}

	_, err = w.writer.Execute(t.Context(), w.depositWrite(t, unknown, account, 100))
	if !errors.Is(err, idempotency.ErrOperationVoided) {
		t.Fatalf("a late request after the tombstone: got %v, want ErrOperationVoided", err)
	}

	if got := w.bank.transactionsWithClientRef(t, unknown); got != 0 {
		t.Fatalf("the voided key applied %d times", got)
	}

	appliedWrite := w.depositWrite(t, applied, account, 100)

	first, err := w.writer.Execute(t.Context(), appliedWrite)
	if err != nil {
		t.Fatal(err)
	}

	resolution, err = w.resolver.Resolve(t.Context(), resolveOf(appliedWrite))

	isApplied := err == nil && resolution.Outcome == idempotency.OutcomeApplied && resolution.Response != nil && bytes.Equal(resolution.Response.Body, first.Body)
	if !isApplied {
		t.Fatalf("applied key: %+v, %v", resolution, err)
	}

	again, err := w.resolver.Resolve(t.Context(), resolveOf(w.depositWrite(t, unknown, account, 100)))
	if err != nil || again.Outcome != idempotency.OutcomeVoided {
		t.Fatalf("a second resolve changed its mind: %+v, %v", again, err)
	}
}

// TestResolve_KeyTakenByAnotherRequest_IsSuperseded guards a known failure
// under resolve: a buggy client reused an idle key with another body, which
// applied. Resolving the first request must not answer "applied" for it.
func TestResolve_KeyTakenByAnotherRequest_IsSuperseded(t *testing.T) {
	w := newWrites(t)
	account := w.bank.newCustomer(t)
	key := newKey()
	original := w.depositWrite(t, key, account, 100)

	_, err := w.writer.Execute(t.Context(), w.depositWrite(t, key, account, 250))
	if err != nil {
		t.Fatal(err)
	}

	resolution, err := w.resolver.Resolve(t.Context(), resolveOf(original))
	if err != nil || resolution.Outcome != idempotency.OutcomeSuperseded || resolution.Response != nil {
		t.Fatalf("the original request: %+v, %v", resolution, err)
	}

	if got := w.bank.transactionsWithClientRef(t, key); got != 1 {
		t.Fatalf("the key applied %d times", got)
	}
}

func resolveOf(write application.Write) application.ResolveRequest {
	return application.ResolveRequest{Key: write.Key, Endpoint: write.Endpoint, RequestHash: write.RequestHash}
}

func TestIdempotencyKeys_NeverCommitPending(t *testing.T) {
	w := newWrites(t)

	err := pgx.BeginTxFunc(t.Context(), w.bank.pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `INSERT INTO idempotency_keys (key, endpoint, request_hash, status) VALUES ($1, '/v1/deposits', '\x00', 'pending')`, string(newKey()))

		return err
	})

	var pgErr *pgconn.PgError

	isRefused := errors.As(err, &pgErr) && pgErr.Code == checkViolation
	if !isRefused {
		t.Fatalf("got %v, want the commit refused", err)
	}
}

func TestDeleteExpiredKeys_RemovesOnlyExpiredOnes(t *testing.T) {
	w := newWrites(t)
	account := w.bank.newCustomer(t)
	expired, fresh := newKey(), newKey()

	for _, key := range []idempotency.Key{expired, fresh} {
		_, err := w.writer.Execute(t.Context(), w.depositWrite(t, key, account, 10))
		if err != nil {
			t.Fatal(err)
		}
	}

	migrator := newPool(t, database.migratorURL)

	_, err := migrator.Exec(t.Context(), `UPDATE idempotency_keys SET expires_at = now() - interval '2 hours' WHERE key = $1`, string(expired))
	if err != nil {
		t.Fatal(err)
	}

	_, err = postgres.NewLedgerStore(w.bank.pool).DeleteExpiredKeys(t.Context(), 1_000)
	if err != nil {
		t.Fatal(err)
	}

	resolution, err := w.resolver.Resolve(t.Context(), resolveOf(w.depositWrite(t, fresh, account, 10)))
	if err != nil || resolution.Outcome != idempotency.OutcomeApplied {
		t.Fatalf("the fresh key was deleted: %+v, %v", resolution, err)
	}

	var remaining int

	err = migrator.QueryRow(t.Context(), `SELECT count(*) FROM idempotency_keys WHERE key = $1`, string(expired)).Scan(&remaining)
	if err != nil || remaining != 0 {
		t.Fatalf("the expired key is still there (%d, %v)", remaining, err)
	}
}

// keyVersion is the transaction that wrote the key's current row version.
func (w writes) keyVersion(t *testing.T, key idempotency.Key) string {
	t.Helper()

	var xmin string

	err := w.bank.pool.QueryRow(t.Context(), `SELECT xmin::text FROM idempotency_keys WHERE key = $1`, string(key)).Scan(&xmin)
	if err != nil {
		t.Fatal(err)
	}

	return xmin
}

// TestReplay_WritesBeforeAnswering is a regression test: an answer read from a
// commit whose wait for the synchronous standby was cancelled may not be on the
// replica. The replay and resolve both write the key, so their own commit waits
// for the standby before the answer goes out.
func TestReplay_WritesBeforeAnswering(t *testing.T) {
	w := newWrites(t)
	account := w.bank.newCustomer(t)
	key := newKey()

	deposit := w.depositWrite(t, key, account, 300)

	_, err := w.writer.Execute(t.Context(), deposit)
	if err != nil {
		t.Fatal(err)
	}

	stored := w.keyVersion(t, key)

	_, err = w.writer.Execute(t.Context(), deposit)
	if err != nil {
		t.Fatal(err)
	}

	replayed := w.keyVersion(t, key)
	if replayed == stored {
		t.Fatal("the replay answered without writing")
	}

	_, err = w.resolver.Resolve(t.Context(), resolveOf(deposit))
	if err != nil {
		t.Fatal(err)
	}

	if w.keyVersion(t, key) == replayed {
		t.Fatal("resolve answered without writing")
	}
}
