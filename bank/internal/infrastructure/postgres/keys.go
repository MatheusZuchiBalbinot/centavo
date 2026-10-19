package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
)

// lockNotAvailable is lock_timeout firing while another request owns the key.
const lockNotAvailable = "55P03"

const (
	claimKeySQL = `
INSERT INTO idempotency_keys (key, endpoint, request_hash, status)
VALUES ($1, $2, $3, 'pending')
ON CONFLICT (key) DO NOTHING
RETURNING key`

	voidKeySQL = `
INSERT INTO idempotency_keys (key, status)
VALUES ($1, 'voided')
ON CONFLICT (key) DO NOTHING
RETURNING key`

	readKeySQL = `
SELECT coalesce(endpoint, ''), coalesce(request_hash, ''::bytea), status::text,
       coalesce(response_status, 0), coalesce(response_body, ''::bytea)
FROM idempotency_keys
WHERE key = $1`

	completeKeySQL = `
UPDATE idempotency_keys
SET status = 'completed', operation_id = $2, response_status = $3, response_body = $4
WHERE key = $1 AND status = 'pending'`
)

var errKeyNotPending = errors.New("completing an idempotency key that this unit of work does not own")

const touchKeySQL = `UPDATE idempotency_keys SET expires_at = expires_at WHERE key = $1`

const clientRefAppliedSQL = `SELECT EXISTS (SELECT 1 FROM ledger_transactions WHERE client_ref = $1)`

// deleteExpiredKeysSQL keeps an hour of margin past the 24 hours of idempotency
// keys live 24 hours by the database clock and measures both with the database
// clock, immune to any worker's clock.
const deleteExpiredKeysSQL = `
DELETE FROM idempotency_keys
WHERE key IN (
    SELECT key FROM idempotency_keys
    WHERE expires_at < now() - interval '1 hour'
    ORDER BY expires_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)`

// keyStore keeps idempotency keys.
type keyStore struct {
	db querier
}

// ClaimKey inserts the key as pending. If another request holds it, the
// INSERT waits on the primary key until that request commits; then the key
// exists and the stored record comes back instead.
func (s keyStore) ClaimKey(ctx context.Context, claim application.KeyClaim) (idempotency.Record, bool, error) {
	var inserted string

	insert := newStatement("claiming idempotency key", claimKeySQL, string(claim.Key), claim.Endpoint, claim.RequestHash[:])

	isClaimed, err := scanOptionalRow(ctx, s.db, insert, &inserted)
	if err != nil {
		return idempotency.Record{}, false, keyError(err)
	}

	if isClaimed {
		return idempotency.Record{}, true, nil
	}

	record, err := s.readKey(ctx, claim.Key)

	return record, false, err
}

// TouchKey writes a new version of the key's row with the same values.
func (s keyStore) TouchKey(ctx context.Context, key idempotency.Key) error {
	_, err := execute(ctx, s.db, newStatement("touching idempotency key", touchKeySQL, string(key)))

	return keyError(err)
}

// VoidKey leaves a tombstone, or returns the record that is already there.
func (s keyStore) VoidKey(ctx context.Context, key idempotency.Key) (idempotency.Record, bool, error) {
	var inserted string

	isVoided, err := scanOptionalRow(ctx, s.db, newStatement("voiding idempotency key", voidKeySQL, string(key)), &inserted)
	if err != nil {
		return idempotency.Record{}, false, keyError(err)
	}

	if isVoided {
		return idempotency.Record{Key: key, Status: idempotency.StatusVoided}, true, nil
	}

	record, err := s.readKey(ctx, key)

	return record, false, err
}

// CompleteKey stores the answer in the same transaction as the effect.
func (s keyStore) CompleteKey(ctx context.Context, key idempotency.Key, operationID ledger.OperationID, response idempotency.StoredResponse) error {
	complete := newStatement("completing idempotency key", completeKeySQL, string(key), uuid.UUID(operationID), response.Status, response.Body)

	return executeOne(ctx, s.db, complete, errKeyNotPending)
}

// IsClientRefApplied tells whether any ledger transaction carries the key.
// The naive resolve answers with it, with no tombstone to stop a late request.
func (s keyStore) IsClientRefApplied(ctx context.Context, key idempotency.Key) (bool, error) {
	var isApplied bool

	err := scanRow(ctx, s.db, newStatement("looking up client_ref", clientRefAppliedSQL, string(key)), &isApplied)

	return isApplied, err
}

// DeleteExpiredKeys removes one batch of expired keys and reports how many.
func (s keyStore) DeleteExpiredKeys(ctx context.Context, batch int) (int64, error) {
	return execute(ctx, s.db, newStatement("deleting expired idempotency keys", deleteExpiredKeysSQL, batch))
}

func (s keyStore) readKey(ctx context.Context, key idempotency.Key) (idempotency.Record, error) {
	record := idempotency.Record{Key: key}

	var (
		hash   []byte
		status string
	)

	read := newStatement("reading idempotency key", readKeySQL, string(key))

	err := scanRow(ctx, s.db, read, &record.Endpoint, &hash, &status, &record.Response.Status, &record.Response.Body)
	if err != nil {
		return idempotency.Record{}, err
	}

	record.Status = idempotency.Status(status)
	copy(record.RequestHash[:], hash)

	return record, nil
}

// keyError turns a lock timeout on the key into "another request is running":
// its outcome is unknown, and the client should retry with the same key.
func keyError(err error) error {
	var pgErr *pgconn.PgError

	isWaitingForOwner := errors.As(err, &pgErr) && pgErr.Code == lockNotAvailable
	if isWaitingForOwner {
		return idempotency.ErrRequestInProgress
	}

	return err
}
