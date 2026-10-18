// Package truth is the partner's own record of what it did, in SQLite with
// synchronous=FULL, a failure domain apart from the bank's PostgreSQL
// (docs/decisions/ADR-014). The partner may lie in answers and deliveries;
// it never lies here. Double processing is the one exception: it is a bug
// of the partner itself, so it changes the truth.
package truth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Status is the authoritative status of a transfer (docs/architecture.md, section 4.4).
type Status string

// The statuses of a transfer at the partner.
const (
	StatusProcessing Status = "processing"
	StatusSettled    Status = "settled"
	StatusRejected   Status = "rejected"
	StatusReturned   Status = "returned"
)

// EntryKind is a line of the participant's statement.
type EntryKind string

// The kinds of statement line.
const (
	EntrySettlement EntryKind = "settlement"
	EntryReturn     EntryKind = "return"
	EntryIncoming   EntryKind = "incoming"
)

// EventType is the type of a webhook.
type EventType string

// The webhook event types.
const (
	EventSettled  EventType = "transfer.settled"
	EventRejected EventType = "transfer.rejected"
	EventReturned EventType = "transfer.returned"
	EventIncoming EventType = "incoming.received"
)

// DeliveryStatus is where a webhook delivery stands.
type DeliveryStatus string

// The states of a webhook delivery.
const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliveryDelivered DeliveryStatus = "delivered"
	// DeliveryAbandoned is a provider giving up after its retries, or a dropped webhook.
	DeliveryAbandoned DeliveryStatus = "abandoned"
)

// ErrNotFound means the partner has no record of the transfer.
var ErrNotFound = errors.New("not found")

// Transfer is a PIX the partner received.
type Transfer struct {
	EndToEndID     string
	ParticipantID  string
	AmountCents    int64
	DestinationKey string
	Status         Status
	RejectReason   string
	// IsDoubleProcessed marks a transfer the partner will settle twice.
	IsDoubleProcessed bool
	SettleAt          time.Time
	ReturnAt          *time.Time
	CreatedAt         time.Time
	// CorrelationID is the submitter's, echoed on every webhook of the transfer.
	CorrelationID string
}

// StatementEntry is a line of a participant's statement, ordered by Seq.
type StatementEntry struct {
	Seq           int64     `json:"seq"`
	ParticipantID string    `json:"participant_id"`
	Kind          EntryKind `json:"kind"`
	EndToEndID    string    `json:"end_to_end_id"`
	AmountCents   int64     `json:"amount_cents"`
	CreatedAt     time.Time `json:"created_at"`
	// DestinationAccountID is the account an incoming entry credits.
	DestinationAccountID string `json:"destination_account_id,omitempty"`
	// CorrelationID is the one the PIX came with, so the participant's
	// reconciliation can follow the PIX in its logs and failpoints.
	CorrelationID string `json:"correlation_id,omitempty"`
}

// Incoming is a PIX the partner credited to a participant.
type Incoming struct {
	EndToEndID    string
	ParticipantID string
	AccountID     string
	AmountCents   int64
	CreatedAt     time.Time
	// CorrelationID is the sender's, echoed on the webhook.
	CorrelationID string
}

// Event is one webhook the partner must deliver.
type Event struct {
	ID            string    `json:"event_id"`
	Type          EventType `json:"type"`
	EndToEndID    string    `json:"end_to_end_id"`
	AmountCents   int64     `json:"amount_cents"`
	Sequence      int       `json:"sequence"`
	OccurredAt    time.Time `json:"occurred_at"`
	Reason        string    `json:"reason,omitempty"`
	AccountID     string    `json:"destination_account_id,omitempty"`
	ParticipantID string    `json:"-"`
	CorrelationID string    `json:"-"`
}

// Delivery is one attempt plan for an event; lies can add copies.
type Delivery struct {
	ID            int64
	EventID       string
	ParticipantID string
	Payload       []byte
	Attempts      int
	NextAttemptAt time.Time
	Status        DeliveryStatus
	CorrelationID string
}

// PlannedDelivery is a copy of an event to send at a given time.
type PlannedDelivery struct {
	EventID string
	At      time.Time
	// IsDropped records the delivery as abandoned without sending it.
	IsDropped bool
}

// Planner decides how an event is delivered: once and now when honest, or
// late, twice, with a new id or never, when the partner lies.
type Planner func(event Event, now time.Time) []PlannedDelivery

// Store is the partner's truth.
type Store struct {
	db *sql.DB
}

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

// columnUpgrade is a column a later schema added. CREATE TABLE IF NOT EXISTS
// leaves a truth created before it as it was, so Open adds what is missing.
type columnUpgrade struct {
	table      string
	column     string
	definition string
}

var columnUpgrades = []columnUpgrade{
	{table: "transfers", column: "correlation_id", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "incoming", column: "correlation_id", definition: "TEXT NOT NULL DEFAULT ''"},
	{table: "deliveries", column: "correlation_id", definition: "TEXT NOT NULL DEFAULT ''"},
}

// Open opens (or creates) the truth at path. synchronous=FULL makes every
// commit durable before it returns; a single connection serializes writers.
func Open(ctx context.Context, path string) (*Store, error) {
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

	return &Store{db: db}, nil
}

func upgradeColumns(ctx context.Context, db *sql.DB) error {
	for _, upgrade := range columnUpgrades {
		var count int

		err := db.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, upgrade.table, upgrade.column).Scan(&count)
		if err != nil {
			return fmt.Errorf("reading the columns of %s: %w", upgrade.table, err)
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

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// SubmitResult is what POST /v1/transfers found or created.
type SubmitResult struct {
	Transfer Transfer
	IsNew    bool
}

const insertTransferSQL = `
INSERT INTO transfers (end_to_end_id, participant_id, amount_cents, destination_key, status, reject_reason, is_double, settle_at, created_at, correlation_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

// Submit records a new transfer, or returns the one with the same
// end_to_end_id: the API is idempotent by end_to_end_id.
func (s *Store) Submit(ctx context.Context, transfer Transfer) (SubmitResult, error) {
	var result SubmitResult

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		existing, err := getTransfer(ctx, tx, transfer.EndToEndID)
		if err == nil {
			result = SubmitResult{Transfer: existing}

			return nil
		}

		if !errors.Is(err, ErrNotFound) {
			return err
		}

		_, err = tx.ExecContext(
			ctx,
			insertTransferSQL,
			transfer.EndToEndID,
			transfer.ParticipantID,
			transfer.AmountCents,
			transfer.DestinationKey,
			transfer.Status,
			transfer.RejectReason,
			transfer.IsDoubleProcessed,
			transfer.SettleAt.UnixNano(),
			transfer.CreatedAt.UnixNano(),
			transfer.CorrelationID,
		)
		if err != nil {
			return fmt.Errorf("recording a transfer: %w", err)
		}

		result = SubmitResult{Transfer: transfer, IsNew: true}

		return nil
	})

	return result, err
}

// Get returns the authoritative state of a transfer.
func (s *Store) Get(ctx context.Context, endToEndID string) (Transfer, error) {
	return getTransfer(ctx, s.db, endToEndID)
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const selectTransferSQL = `
SELECT end_to_end_id, participant_id, amount_cents, destination_key, status, reject_reason, is_double, settle_at, return_at, created_at, correlation_id
FROM transfers
WHERE end_to_end_id = ?`

func getTransfer(ctx context.Context, q querier, endToEndID string) (Transfer, error) {
	var (
		transfer          Transfer
		settleAt, created int64
		returnAt          sql.NullInt64
	)

	row := q.QueryRowContext(
		ctx,
		selectTransferSQL,
		endToEndID,
	)

	err := row.Scan(
		&transfer.EndToEndID,
		&transfer.ParticipantID,
		&transfer.AmountCents,
		&transfer.DestinationKey,
		&transfer.Status,
		&transfer.RejectReason,
		&transfer.IsDoubleProcessed,
		&settleAt,
		&returnAt,
		&created,
		&transfer.CorrelationID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Transfer{}, ErrNotFound
	}

	if err != nil {
		return Transfer{}, fmt.Errorf("reading a transfer: %w", err)
	}

	transfer.SettleAt = time.Unix(0, settleAt).UTC()
	transfer.CreatedAt = time.Unix(0, created).UTC()

	if returnAt.Valid {
		at := time.Unix(0, returnAt.Int64).UTC()
		transfer.ReturnAt = &at
	}

	return transfer, nil
}

// Due lists transfers waiting for a decision: processing ones whose settle
// time came, and settled ones whose return time came.
func (s *Store) Due(ctx context.Context, now time.Time, limit int) ([]Transfer, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT end_to_end_id FROM transfers
WHERE (status = 'processing' AND settle_at <= ?) OR (status = 'settled' AND return_at IS NOT NULL AND return_at <= ?)
ORDER BY settle_at LIMIT ?`, now.UnixNano(), now.UnixNano(), limit)
	if err != nil {
		return nil, fmt.Errorf("listing due transfers: %w", err)
	}
	defer rows.Close()

	var ids []string

	for rows.Next() {
		var id string

		err := rows.Scan(&id)
		if err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}

	err = rows.Err()
	if err != nil {
		return nil, err
	}

	transfers := make([]Transfer, 0, len(ids))

	for _, id := range ids {
		transfer, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}

		transfers = append(transfers, transfer)
	}

	return transfers, nil
}

// SettleCommand settles a transfer.
type SettleCommand struct {
	EndToEndID string
	Now        time.Time
	// ReturnAt schedules a return after the settlement, when the partner lies.
	ReturnAt *time.Time
	EventID  string
	Planner  Planner
}

// Settle moves the money out of the participant's reserve: one statement
// entry, or two for a transfer the partner processes twice by mistake.
func (s *Store) Settle(ctx context.Context, command SettleCommand) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		transfer, err := getTransfer(ctx, tx, command.EndToEndID)
		if err != nil {
			return err
		}

		if transfer.Status != StatusProcessing {
			return nil
		}

		var returnAt any
		if command.ReturnAt != nil {
			returnAt = command.ReturnAt.UnixNano()
		}

		_, err = tx.ExecContext(ctx, `UPDATE transfers SET status = 'settled', return_at = ? WHERE end_to_end_id = ?`, returnAt, transfer.EndToEndID)
		if err != nil {
			return err
		}

		settlements := 1
		if transfer.IsDoubleProcessed {
			settlements = 2
		}

		for range settlements {
			statementEntry := StatementEntry{
				ParticipantID: transfer.ParticipantID,
				Kind:          EntrySettlement,
				EndToEndID:    transfer.EndToEndID,
				AmountCents:   transfer.AmountCents,
				CreatedAt:     command.Now,
			}

			err = addEntry(ctx, tx, statementEntry)
			if err != nil {
				return err
			}
		}

		event := Event{
			ID:            command.EventID,
			Type:          EventSettled,
			EndToEndID:    transfer.EndToEndID,
			AmountCents:   transfer.AmountCents,
			OccurredAt:    command.Now,
			ParticipantID: transfer.ParticipantID,
			CorrelationID: transfer.CorrelationID,
		}

		return enqueue(ctx, tx, event, command.Planner)
	})
}

// RejectCommand rejects a transfer that is still processing.
type RejectCommand struct {
	EndToEndID string
	Reason     string
	Now        time.Time
	EventID    string
	Planner    Planner
}

// Reject is a definitive refusal: nothing leaves the reserve.
func (s *Store) Reject(ctx context.Context, command RejectCommand) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		transfer, err := getTransfer(ctx, tx, command.EndToEndID)
		if err != nil {
			return err
		}

		if transfer.Status != StatusProcessing {
			return nil
		}

		_, err = tx.ExecContext(ctx, `UPDATE transfers SET status = 'rejected', reject_reason = ? WHERE end_to_end_id = ?`, command.Reason, transfer.EndToEndID)
		if err != nil {
			return err
		}

		event := Event{
			ID:            command.EventID,
			Type:          EventRejected,
			EndToEndID:    transfer.EndToEndID,
			AmountCents:   transfer.AmountCents,
			OccurredAt:    command.Now,
			Reason:        command.Reason,
			ParticipantID: transfer.ParticipantID,
			CorrelationID: transfer.CorrelationID,
		}

		return enqueue(ctx, tx, event, command.Planner)
	})
}

// ReturnCommand returns a settled transfer.
type ReturnCommand struct {
	EndToEndID string
	Now        time.Time
	EventID    string
	Planner    Planner
}

// Return gives a settled transfer's money back to the reserve.
func (s *Store) Return(ctx context.Context, command ReturnCommand) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		transfer, err := getTransfer(ctx, tx, command.EndToEndID)
		if err != nil {
			return err
		}

		if transfer.Status != StatusSettled {
			return nil
		}

		_, err = tx.ExecContext(ctx, `UPDATE transfers SET status = 'returned', return_at = NULL WHERE end_to_end_id = ?`, transfer.EndToEndID)
		if err != nil {
			return err
		}

		statementEntry := StatementEntry{
			ParticipantID: transfer.ParticipantID,
			Kind:          EntryReturn,
			EndToEndID:    transfer.EndToEndID,
			AmountCents:   transfer.AmountCents,
			CreatedAt:     command.Now,
		}

		err = addEntry(ctx, tx, statementEntry)
		if err != nil {
			return err
		}

		event := Event{
			ID:            command.EventID,
			Type:          EventReturned,
			EndToEndID:    transfer.EndToEndID,
			AmountCents:   transfer.AmountCents,
			OccurredAt:    command.Now,
			ParticipantID: transfer.ParticipantID,
			CorrelationID: transfer.CorrelationID,
		}

		return enqueue(ctx, tx, event, command.Planner)
	})
}

// IncomingCommand credits an incoming PIX to a participant.
type IncomingCommand struct {
	Incoming Incoming
	EventID  string
	Planner  Planner
}

const insertIncomingSQL = `
INSERT INTO incoming (end_to_end_id, participant_id, account_id, amount_cents, created_at, correlation_id)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (end_to_end_id) DO NOTHING`

// AddIncoming records an incoming PIX once per end_to_end_id and reports
// whether it was new.
func (s *Store) AddIncoming(ctx context.Context, command IncomingCommand) (bool, error) {
	isNew := false

	err := s.inTx(ctx, func(tx *sql.Tx) error {
		incoming := command.Incoming

		result, err := tx.ExecContext(ctx, insertIncomingSQL,
			incoming.EndToEndID, incoming.ParticipantID, incoming.AccountID, incoming.AmountCents, incoming.CreatedAt.UnixNano(), incoming.CorrelationID)
		if err != nil {
			return err
		}

		inserted, err := result.RowsAffected()
		if err != nil || inserted == 0 {
			return err
		}

		isNew = true

		statementEntry := StatementEntry{
			ParticipantID: incoming.ParticipantID,
			Kind:          EntryIncoming,
			EndToEndID:    incoming.EndToEndID,
			AmountCents:   incoming.AmountCents,
			CreatedAt:     incoming.CreatedAt,
		}

		err = addEntry(ctx, tx, statementEntry)
		if err != nil {
			return err
		}

		event := Event{
			ID:            command.EventID,
			Type:          EventIncoming,
			EndToEndID:    incoming.EndToEndID,
			AmountCents:   incoming.AmountCents,
			OccurredAt:    incoming.CreatedAt,
			AccountID:     incoming.AccountID,
			ParticipantID: incoming.ParticipantID,
			CorrelationID: incoming.CorrelationID,
		}

		return enqueue(ctx, tx, event, command.Planner)
	})

	return isNew, err
}

func addEntry(ctx context.Context, tx *sql.Tx, entry StatementEntry) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO statement_entries (participant_id, kind, end_to_end_id, amount_cents, created_at) VALUES (?, ?, ?, ?, ?)`,
		entry.ParticipantID, entry.Kind, entry.EndToEndID, entry.AmountCents, entry.CreatedAt.UnixNano())
	if err != nil {
		return fmt.Errorf("recording a statement entry: %w", err)
	}

	return nil
}

// enqueue numbers the event within its transfer and plans its deliveries.
func enqueue(ctx context.Context, tx *sql.Tx, event Event, planner Planner) error {
	isTransferEvent := event.Type != EventIncoming
	if isTransferEvent {
		err := tx.QueryRowContext(ctx, `UPDATE transfers SET webhook_seq = webhook_seq + 1 WHERE end_to_end_id = ? RETURNING webhook_seq`, event.EndToEndID).Scan(
			&event.Sequence,
		)
		if err != nil {
			return fmt.Errorf("numbering a webhook: %w", err)
		}
	}

	for _, planned := range planner(event, event.OccurredAt) {
		copyOf := event
		copyOf.ID = planned.EventID

		payload, err := marshalEvent(copyOf)
		if err != nil {
			return err
		}

		status := DeliveryPending
		if planned.IsDropped {
			status = DeliveryAbandoned
		}

		_, err = tx.ExecContext(ctx, `INSERT INTO deliveries (event_id, participant_id, payload, next_attempt_at, status, correlation_id) VALUES (?, ?, ?, ?, ?, ?)`,
			copyOf.ID, event.ParticipantID, payload, planned.At.UnixNano(), status, event.CorrelationID)
		if err != nil {
			return fmt.Errorf("planning a webhook: %w", err)
		}
	}

	return nil
}

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

// Statement returns the participant's entries after a sequence number.
func (s *Store) Statement(ctx context.Context, participantID string, afterSeq int64, limit int) ([]StatementEntry, error) {
	rows, err := s.db.QueryContext(ctx, statementSQL, participantID, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("reading the statement: %w", err)
	}
	defer rows.Close()

	return scanStatement(rows)
}

func scanStatement(rows *sql.Rows) ([]StatementEntry, error) {
	var entries []StatementEntry

	for rows.Next() {
		var (
			entry   StatementEntry
			created int64
		)

		err := rows.Scan(
			&entry.Seq,
			&entry.ParticipantID,
			&entry.Kind,
			&entry.EndToEndID,
			&entry.AmountCents,
			&created,
			&entry.DestinationAccountID,
			&entry.CorrelationID,
		)
		if err != nil {
			return nil, err
		}

		entry.CreatedAt = time.Unix(0, created).UTC()
		entries = append(entries, entry)
	}

	return entries, rows.Err()
}

const dueDeliveriesSQL = `
SELECT id, event_id, participant_id, payload, attempts, next_attempt_at, correlation_id
FROM deliveries
WHERE status = 'pending' AND next_attempt_at <= ?
ORDER BY next_attempt_at
LIMIT ?`

// DueDeliveries lists pending deliveries whose time came.
func (s *Store) DueDeliveries(ctx context.Context, now time.Time, limit int) ([]Delivery, error) {
	rows, err := s.db.QueryContext(
		ctx,
		dueDeliveriesSQL,
		now.UnixNano(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing due deliveries: %w", err)
	}
	defer rows.Close()

	var deliveries []Delivery

	for rows.Next() {
		var (
			delivery Delivery
			next     int64
		)

		err := rows.Scan(&delivery.ID, &delivery.EventID, &delivery.ParticipantID, &delivery.Payload, &delivery.Attempts, &next, &delivery.CorrelationID)
		if err != nil {
			return nil, err
		}

		delivery.NextAttemptAt = time.Unix(0, next).UTC()
		delivery.Status = DeliveryPending
		deliveries = append(deliveries, delivery)
	}

	return deliveries, rows.Err()
}

// MarkDelivered closes a delivery.
func (s *Store) MarkDelivered(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE deliveries SET status = 'delivered', attempts = attempts + 1 WHERE id = ?`, id)

	return err
}

// Reschedule records a failed attempt; past the last attempt the delivery is abandoned.
func (s *Store) Reschedule(ctx context.Context, id int64, next time.Time, isLastAttempt bool) error {
	status := DeliveryPending
	if isLastAttempt {
		status = DeliveryAbandoned
	}

	_, err := s.db.ExecContext(ctx, `UPDATE deliveries SET attempts = attempts + 1, next_attempt_at = ?, status = ? WHERE id = ?`, next.UnixNano(), status, id)

	return err
}

// PendingDeliveries counts what is still to be sent, for the lab's quiescence.
func (s *Store) PendingDeliveries(ctx context.Context) (int, error) {
	var count int

	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM deliveries WHERE status = 'pending'`).Scan(&count)

	return count, err
}

// LastStatementSeq is the participant's newest statement entry, or zero:
// the lab's quiescence waits until the bank reconciled up to it.
func (s *Store) LastStatementSeq(ctx context.Context, participantID string) (int64, error) {
	var seq int64

	err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(seq), 0) FROM statement_entries WHERE participant_id = ?`, participantID).Scan(&seq)

	return seq, err
}

func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting a truth transaction: %w", err)
	}

	err = fn(tx)
	if err != nil {
		return errors.Join(err, tx.Rollback())
	}

	return tx.Commit()
}
