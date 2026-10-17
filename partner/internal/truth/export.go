package truth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// TransferView is a transfer as the lab sees it in the truth.
type TransferView struct {
	EndToEndID        string    `json:"end_to_end_id"`
	ParticipantID     string    `json:"participant_id"`
	AmountCents       int64     `json:"amount_cents"`
	DestinationKey    string    `json:"destination_key"`
	Status            Status    `json:"status"`
	RejectReason      string    `json:"reject_reason,omitempty"`
	IsDoubleProcessed bool      `json:"double_processed"`
	CreatedAt         time.Time `json:"created_at"`
}

// IncomingView is an incoming PIX as the lab sees it in the truth.
type IncomingView struct {
	EndToEndID    string    `json:"end_to_end_id"`
	ParticipantID string    `json:"participant_id"`
	AccountID     string    `json:"account_id"`
	AmountCents   int64     `json:"amount_cents"`
	CreatedAt     time.Time `json:"created_at"`
}

// Export is the whole truth of one participant, double processing included:
// what the checker compares the bank against (docs/invariants-and-checker.md, section 2).
type Export struct {
	ParticipantID string           `json:"participant_id"`
	Transfers     []TransferView   `json:"transfers"`
	Statement     []StatementEntry `json:"statement"`
	Incoming      []IncomingView   `json:"incoming"`
	// PendingDeliveries is how many webhooks are still to be sent.
	PendingDeliveries int `json:"pending_deliveries"`
}

// Truth exports everything about one participant created at or after since:
// a round only needs its own part, and the volume keeps every round's.
func (s *Store) Truth(ctx context.Context, participantID string, since time.Time) (Export, error) {
	export := Export{ParticipantID: participantID, Transfers: []TransferView{}, Statement: []StatementEntry{}, Incoming: []IncomingView{}}

	transfers, err := s.transferViews(ctx, participantID, since)
	if err != nil {
		return Export{}, err
	}

	export.Transfers = transfers

	statement, err := s.statementSince(ctx, participantID, since)
	if err != nil {
		return Export{}, err
	}

	export.Statement = append(export.Statement, statement...)

	export.Incoming, err = s.incoming(ctx, participantID, since)
	if err != nil {
		return Export{}, err
	}

	export.PendingDeliveries, err = s.PendingDeliveries(ctx)

	return export, err
}

func (s *Store) transferViews(ctx context.Context, participantID string, since time.Time) ([]TransferView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT end_to_end_id, participant_id, amount_cents, destination_key, status, reject_reason, is_double, created_at FROM transfers WHERE participant_id = ? AND created_at >= ? ORDER BY created_at, end_to_end_id`, participantID, since.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("exporting transfers: %w", err)
	}
	defer rows.Close()

	views := []TransferView{}

	for rows.Next() {
		var (
			view    TransferView
			created int64
		)

		err := rows.Scan(&view.EndToEndID, &view.ParticipantID, &view.AmountCents, &view.DestinationKey, &view.Status, &view.RejectReason, &view.IsDoubleProcessed, &created)
		if err != nil {
			return nil, err
		}

		view.CreatedAt = time.Unix(0, created).UTC()
		views = append(views, view)
	}

	return views, rows.Err()
}

// statementSinceSQL is statementSQL bounded by time instead of by sequence.
const statementSinceSQL = `
SELECT e.seq, e.participant_id, e.kind, e.end_to_end_id, e.amount_cents, e.created_at, coalesce(i.account_id, ''), coalesce(t.correlation_id, i.correlation_id, '')
FROM statement_entries e
LEFT JOIN incoming i ON e.kind = 'incoming' AND i.end_to_end_id = e.end_to_end_id
LEFT JOIN transfers t ON e.kind IN ('settlement', 'return') AND t.end_to_end_id = e.end_to_end_id
WHERE e.participant_id = ? AND e.created_at >= ?
ORDER BY e.seq`

func (s *Store) statementSince(ctx context.Context, participantID string, since time.Time) ([]StatementEntry, error) {
	rows, err := s.db.QueryContext(ctx, statementSinceSQL, participantID, since.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("exporting the statement: %w", err)
	}
	defer rows.Close()

	return scanStatement(rows)
}

func (s *Store) incoming(ctx context.Context, participantID string, since time.Time) ([]IncomingView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT end_to_end_id, participant_id, account_id, amount_cents, created_at FROM incoming WHERE participant_id = ? AND created_at >= ? ORDER BY created_at, end_to_end_id`, participantID, since.UnixNano())
	if err != nil {
		return nil, fmt.Errorf("exporting incoming: %w", err)
	}
	defer rows.Close()

	views := []IncomingView{}

	for rows.Next() {
		var (
			view    IncomingView
			created int64
		)

		err := rows.Scan(&view.EndToEndID, &view.ParticipantID, &view.AccountID, &view.AmountCents, &created)
		if err != nil {
			return nil, err
		}

		view.CreatedAt = time.Unix(0, created).UTC()
		views = append(views, view)
	}

	return views, rows.Err()
}

func marshalEvent(event Event) ([]byte, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("encoding a webhook: %w", err)
	}

	return payload, nil
}
