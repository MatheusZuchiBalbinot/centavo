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

const idempotencyKeysTable = "idempotency_keys"

var errKeyNotPending = errors.New("completing an idempotency key that this unit of work does not own")

const clientRefAppliedSQL = `SELECT EXISTS (SELECT 1 FROM ledger_transactions WHERE client_ref = @client_ref)`

// Keys live 24 hours by the database clock; this keeps an hour of margin
// past that, measured with the same clock, immune to any worker's.
const deleteExpiredKeysSQL = `
DELETE FROM idempotency_keys
WHERE key IN (
    SELECT key FROM idempotency_keys
    WHERE expires_at < now() - interval '1 hour'
    ORDER BY expires_at
    LIMIT @batch
    FOR UPDATE SKIP LOCKED
)`

type claimedKeyRow struct {
	Key         string `db:"key"`
	Endpoint    string `db:"endpoint"`
	RequestHash []byte `db:"request_hash"`
	Status      string `db:"status"`
}

type voidedKeyRow struct {
	Key    string `db:"key"`
	Status string `db:"status"`
}

// A tombstone has no request or response, read as their zero values.
type keyRecordRow struct {
	Endpoint       string `db:"endpoint"        read:"coalesce(endpoint, '')"`
	RequestHash    []byte `db:"request_hash"    read:"coalesce(request_hash, ''::bytea)"`
	Status         string `db:"status"          read:"status::text"`
	ResponseStatus int    `db:"response_status" read:"coalesce(response_status, 0)"`
	ResponseBody   []byte `db:"response_body"   read:"coalesce(response_body, ''::bytea)"`
}

type keyStore struct {
	db querier
}

// If another request holds the key, the INSERT waits on the primary key
// until that request commits; then the key exists and the stored record
// comes back instead.
func (s keyStore) ClaimKey(ctx context.Context, claim application.KeyClaim) (idempotency.Record, bool, error) {
	var inserted string

	row := claimedKeyRow{
		Key: string(claim.Key), Endpoint: claim.Endpoint, RequestHash: claim.RequestHash[:], Status: string(idempotency.StatusPending),
	}

	isClaimed, err := insertRow("claiming idempotency key", idempotencyKeysTable, row).
		OnConflictDoNothing("(key)").
		Returning("key").
		ScanOptional(ctx, s.db, &inserted)
	if err != nil {
		return idempotency.Record{}, false, keyError(err)
	}

	if isClaimed {
		return idempotency.Record{}, true, nil
	}

	record, err := s.readKey(ctx, claim.Key)

	return record, false, err
}

func (s keyStore) TouchKey(ctx context.Context, key idempotency.Key) error {
	_, err := update("touching idempotency key", idempotencyKeysTable).
		SetSQL("expires_at = expires_at", nil).
		Where("key = @key", args{"key": string(key)}).
		Exec(ctx, s.db)

	return keyError(err)
}

func (s keyStore) VoidKey(ctx context.Context, key idempotency.Key) (idempotency.Record, bool, error) {
	var inserted string

	row := voidedKeyRow{Key: string(key), Status: string(idempotency.StatusVoided)}

	isVoided, err := insertRow("voiding idempotency key", idempotencyKeysTable, row).
		OnConflictDoNothing("(key)").
		Returning("key").
		ScanOptional(ctx, s.db, &inserted)
	if err != nil {
		return idempotency.Record{}, false, keyError(err)
	}

	if isVoided {
		return idempotency.Record{Key: key, Status: idempotency.StatusVoided}, true, nil
	}

	record, err := s.readKey(ctx, key)

	return record, false, err
}

func (s keyStore) CompleteKey(ctx context.Context, key idempotency.Key, operationID ledger.OperationID, response idempotency.StoredResponse) error {
	return update("completing idempotency key", idempotencyKeysTable).
		Set("status", string(idempotency.StatusCompleted)).
		Set("operation_id", uuid.UUID(operationID)).
		Set("response_status", response.Status).
		Set("response_body", response.Body).
		Where("key = @key AND status = 'pending'", args{"key": string(key)}).
		ExecOne(ctx, s.db, errKeyNotPending)
}

// The naive resolve answers with it, with no tombstone to stop a late
// request.
func (s keyStore) IsClientRefApplied(ctx context.Context, key idempotency.Key) (bool, error) {
	var isApplied bool

	query := newStatement("looking up client_ref", clientRefAppliedSQL, args{"client_ref": string(key)})
	err := scanRow(ctx, s.db, query, &isApplied)

	return isApplied, err
}

func (s keyStore) DeleteExpiredKeys(ctx context.Context, batch int) (int64, error) {
	return execute(ctx, s.db, newStatement("deleting expired idempotency keys", deleteExpiredKeysSQL, args{"batch": batch}))
}

func (s keyStore) readKey(ctx context.Context, key idempotency.Key) (idempotency.Record, error) {
	row, err := selectFrom[keyRecordRow]("reading idempotency key", idempotencyKeysTable).
		Where("key = @key", args{"key": string(key)}).
		One(ctx, s.db)
	if err != nil {
		return idempotency.Record{}, err
	}

	record := idempotency.Record{
		Key: key, Endpoint: row.Endpoint, Status: idempotency.Status(row.Status),
		Response: idempotency.StoredResponse{Status: row.ResponseStatus, Body: row.ResponseBody},
	}
	copy(record.RequestHash[:], row.RequestHash)

	return record, nil
}

// A lock timeout on the key means another request is running: its outcome
// is unknown, and the client should retry with the same key.
func keyError(err error) error {
	var pgErr *pgconn.PgError

	isWaitingForOwner := errors.As(err, &pgErr) && pgErr.Code == lockNotAvailable
	if isWaitingForOwner {
		return idempotency.ErrRequestInProgress
	}

	return err
}
