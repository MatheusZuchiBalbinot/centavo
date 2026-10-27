// Package storage keeps the partner's truth in SQLite with synchronous=FULL,
// a failure domain apart from the bank's PostgreSQL.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"centavo/partner/internal/truth"
)

const schema = `
CREATE TABLE IF NOT EXISTS transfers (
    end_to_end_id      TEXT PRIMARY KEY,
    participant_id     TEXT NOT NULL,
    amount_cents       INTEGER NOT NULL CHECK (amount_cents > 0),
    destination_key    TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('processing', 'settled', 'rejected', 'returned')),
    reject_reason      TEXT NOT NULL DEFAULT '',
    is_double          INTEGER NOT NULL DEFAULT 0,
    webhook_seq        INTEGER NOT NULL DEFAULT 0,
    settle_at          INTEGER NOT NULL,
    return_at          INTEGER,
    created_at         INTEGER NOT NULL,
    correlation_id     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS transfers_due ON transfers (status, settle_at);

CREATE TABLE IF NOT EXISTS statement_entries (
    seq             INTEGER PRIMARY KEY AUTOINCREMENT,
    participant_id  TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('settlement', 'return', 'incoming')),
    end_to_end_id   TEXT NOT NULL,
    amount_cents    INTEGER NOT NULL,
    created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS statement_by_participant ON statement_entries (participant_id, seq);

CREATE TABLE IF NOT EXISTS incoming (
    end_to_end_id   TEXT PRIMARY KEY,
    participant_id  TEXT NOT NULL,
    account_id      TEXT NOT NULL,
    amount_cents    INTEGER NOT NULL CHECK (amount_cents > 0),
    created_at      INTEGER NOT NULL,
    correlation_id  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS deliveries (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id         TEXT NOT NULL,
    participant_id   TEXT NOT NULL,
    payload          BLOB NOT NULL,
    attempts         INTEGER NOT NULL DEFAULT 0,
    next_attempt_at  INTEGER NOT NULL,
    status           TEXT NOT NULL CHECK (status IN ('pending', 'delivered', 'abandoned')),
    correlation_id   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS deliveries_due ON deliveries (status, next_attempt_at);
`

var columnUpgrades = []columnUpgrade{
	{table: "transfers", column: "correlation_id", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "incoming", column: "correlation_id", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "deliveries", column: "correlation_id", definition: "TEXT NOT NULL DEFAULT ''"},
}

const insertTransferSQL = `
INSERT INTO transfers (end_to_end_id, participant_id, amount_cents, destination_key, status, reject_reason, is_double, settle_at, created_at, correlation_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const selectTransferSQL = `
SELECT end_to_end_id, participant_id, amount_cents, destination_key, status, reject_reason, is_double, settle_at, return_at, created_at, correlation_id
FROM transfers
WHERE end_to_end_id = ?`

const insertIncomingSQL = `
INSERT INTO incoming (end_to_end_id, participant_id, account_id, amount_cents, created_at, correlation_id)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (end_to_end_id) DO NOTHING`

// statementSQL joins incoming entries with the account they credit, which a
// reconciler needs to credit a PIX in whose webhook never came, and every
// entry with the correlation id of its PIX.
const statementSQL = `
SELECT e.seq, e.participant_id, e.kind, e.end_to_end_id, e.amount_cents, e.created_at,
       coalesce(i.account_id, ''), coalesce(t.correlation_id, i.correlation_id, '')
FROM statement_entries e
LEFT JOIN incoming i ON e.kind = 'incoming' AND i.end_to_end_id = e.end_to_end_id
LEFT JOIN transfers t ON e.kind IN ('settlement', 'return') AND t.end_to_end_id = e.end_to_end_id
WHERE e.participant_id = ? AND e.seq > ?
ORDER BY e.seq
LIMIT ?`

const dueDeliveriesSQL = `
SELECT id, event_id, participant_id, payload, attempts, next_attempt_at, correlation_id
FROM deliveries
WHERE status = 'pending' AND next_attempt_at <= ?
ORDER BY next_attempt_at
LIMIT ?`

const (
	settleSQL         = `UPDATE transfers SET status = 'settled', return_at = ? WHERE end_to_end_id = ?`
	rejectSQL         = `UPDATE transfers SET status = 'rejected', reject_reason = ? WHERE end_to_end_id = ?`
	returnSQL         = `UPDATE transfers SET status = 'returned', return_at = NULL WHERE end_to_end_id = ?`
	addEntrySQL       = `INSERT INTO statement_entries (participant_id, kind, end_to_end_id, amount_cents, created_at) VALUES (?, ?, ?, ?, ?)`
	nextWebhookSeqSQL = `UPDATE transfers SET webhook_seq = webhook_seq + 1 WHERE end_to_end_id = ? RETURNING webhook_seq`
	addDeliverySQL    = `
INSERT INTO deliveries (event_id, participant_id, payload, next_attempt_at, status, correlation_id)
VALUES (?, ?, ?, ?, ?, ?)`
	columnCountSQL = `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`
)

type DB struct {
	db *sql.DB
}

// columnUpgrade is a column a later schema added. CREATE TABLE IF NOT EXISTS
// leaves a truth created before it as it was, so Open adds what is missing.
type columnUpgrade struct {
	table      string
	column     string
	definition string
}

type tx struct {
	db querier
}

// synchronous=FULL makes every commit durable before it returns; a single
// connection serializes writers.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=synchronous(FULL)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate", path)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening the truth: %w", err)
	}

	db.SetMaxOpenConns(1)

	_, err = db.ExecContext(ctx, schema)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("creating the truth schema: %w", err), db.Close())
	}

	err = upgradeColumns(ctx, db)
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}

	return &DB{db: db}, nil
}

func (d *DB) Close() error {
	return d.db.Close()
}

func (d *DB) InTx(ctx context.Context, fn func(ctx context.Context, tx truth.Tx) error) error {
	sqlTx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting a truth transaction: %w", err)
	}

	err = fn(ctx, tx{db: sqlTx})
	if err != nil {
		return errors.Join(err, sqlTx.Rollback())
	}

	return sqlTx.Commit()
}

func (t tx) FindTransfer(ctx context.Context, endToEndID string) (truth.Transfer, error) {
	return findTransfer(ctx, t.db, endToEndID)
}

func (t tx) InsertTransfer(ctx context.Context, transfer truth.Transfer) error {
	insert := newStatement(
		"recording a transfer", insertTransferSQL,
		transfer.EndToEndID, transfer.ParticipantID, transfer.AmountCents, transfer.DestinationKey, transfer.Status,
		transfer.RejectReason, transfer.IsDoubleProcessed, transfer.SettleAt.UnixNano(), transfer.CreatedAt.UnixNano(), transfer.CorrelationID,
	)
	_, err := execute(ctx, t.db, insert)

	return err
}

func (t tx) MarkSettled(ctx context.Context, endToEndID string, returnAt *time.Time) error {
	var at sql.NullInt64
	if returnAt != nil {
		at = sql.NullInt64{Int64: returnAt.UnixNano(), Valid: true}
	}

	_, err := execute(ctx, t.db, newStatement("settling a transfer", settleSQL, at, endToEndID))

	return err
}

func (t tx) MarkRejected(ctx context.Context, endToEndID, reason string) error {
	_, err := execute(ctx, t.db, newStatement("rejecting a transfer", rejectSQL, reason, endToEndID))

	return err
}

func (t tx) MarkReturned(ctx context.Context, endToEndID string) error {
	_, err := execute(ctx, t.db, newStatement("returning a transfer", returnSQL, endToEndID))

	return err
}

func (t tx) InsertIncoming(ctx context.Context, incoming truth.Incoming) (bool, error) {
	insert := newStatement(
		"recording an incoming PIX", insertIncomingSQL,
		incoming.EndToEndID, incoming.ParticipantID, incoming.AccountID, incoming.AmountCents, incoming.CreatedAt.UnixNano(), incoming.CorrelationID,
	)
	inserted, err := execute(ctx, t.db, insert)

	return inserted == 1, err
}

func (t tx) AddEntry(ctx context.Context, entry truth.StatementEntry) error {
	add := newStatement(
		"recording a statement entry", addEntrySQL,
		entry.ParticipantID, entry.Kind, entry.EndToEndID, entry.AmountCents, entry.CreatedAt.UnixNano(),
	)
	_, err := execute(ctx, t.db, add)

	return err
}

func (t tx) NextWebhookSeq(ctx context.Context, endToEndID string) (int, error) {
	var seq int

	err := scanRow(ctx, t.db, newStatement("numbering a webhook", nextWebhookSeqSQL, endToEndID), &seq)

	return seq, err
}

func (t tx) AddDelivery(ctx context.Context, delivery truth.DeliveryRecord) error {
	add := newStatement(
		"planning a webhook", addDeliverySQL,
		delivery.EventID, delivery.ParticipantID, delivery.Payload, delivery.At.UnixNano(), delivery.Status, delivery.CorrelationID,
	)
	_, err := execute(ctx, t.db, add)

	return err
}

func upgradeColumns(ctx context.Context, db *sql.DB) error {
	for _, upgrade := range columnUpgrades {
		var count int

		err := scanRow(ctx, db, newStatement("reading the columns of "+upgrade.table, columnCountSQL, upgrade.table, upgrade.column), &count)
		if err != nil {
			return err
		}

		if count > 0 {
			continue
		}

		// The names come from columnUpgrades, never from input.
		_, err = db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", upgrade.table, upgrade.column, upgrade.definition))
		if err != nil {
			return fmt.Errorf("adding %s.%s: %w", upgrade.table, upgrade.column, err)
		}
	}

	return nil
}
