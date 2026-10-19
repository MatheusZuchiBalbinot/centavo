package storage

import (
	"context"
	"database/sql"
	"time"

	"centavo/partner/internal/truth"
)

const exportTransfersSQL = `
SELECT end_to_end_id, participant_id, amount_cents, destination_key, status, reject_reason, is_double, created_at
FROM transfers
WHERE participant_id = ? AND created_at >= ?
ORDER BY created_at, end_to_end_id`

// statementSinceSQL is statementSQL bounded by time instead of by sequence.
const statementSinceSQL = `
SELECT e.seq, e.participant_id, e.kind, e.end_to_end_id, e.amount_cents, e.created_at,
       coalesce(i.account_id, ''), coalesce(t.correlation_id, i.correlation_id, '')
FROM statement_entries e
LEFT JOIN incoming i ON e.kind = 'incoming' AND i.end_to_end_id = e.end_to_end_id
LEFT JOIN transfers t ON e.kind IN ('settlement', 'return') AND t.end_to_end_id = e.end_to_end_id
WHERE e.participant_id = ? AND e.created_at >= ?
ORDER BY e.seq`

const exportIncomingSQL = `
SELECT end_to_end_id, participant_id, account_id, amount_cents, created_at
FROM incoming
WHERE participant_id = ? AND created_at >= ?
ORDER BY created_at, end_to_end_id`

// Truth exports everything about one participant created at or after since:
// a round only needs its own part, and the volume keeps every round's.
func (d *DB) Truth(ctx context.Context, participantID string, since time.Time) (truth.Export, error) {
	export := truth.Export{ParticipantID: participantID}

	var err error

	export.Transfers, err = queryRows(ctx, d.db, newStatement("exporting transfers", exportTransfersSQL, participantID, since.UnixNano()), scanTransferView)
	if err != nil {
		return truth.Export{}, err
	}

	export.Statement, err = queryRows(ctx, d.db, newStatement("exporting the statement", statementSinceSQL, participantID, since.UnixNano()), scanStatementEntry)
	if err != nil {
		return truth.Export{}, err
	}

	export.Incoming, err = queryRows(ctx, d.db, newStatement("exporting incoming", exportIncomingSQL, participantID, since.UnixNano()), scanIncomingView)
	if err != nil {
		return truth.Export{}, err
	}

	export.PendingDeliveries, err = d.PendingDeliveries(ctx)

	return export, err
}

func scanTransferView(rows *sql.Rows) (truth.TransferView, error) {
	var (
		view    truth.TransferView
		created int64
	)

	err := rows.Scan(
		&view.EndToEndID, &view.ParticipantID, &view.AmountCents, &view.DestinationKey, &view.Status, &view.RejectReason,
		&view.IsDoubleProcessed, &created,
	)
	view.CreatedAt = time.Unix(0, created).UTC()

	return view, err
}

func scanIncomingView(rows *sql.Rows) (truth.IncomingView, error) {
	var (
		view    truth.IncomingView
		created int64
	)

	err := rows.Scan(&view.EndToEndID, &view.ParticipantID, &view.AccountID, &view.AmountCents, &created)
	view.CreatedAt = time.Unix(0, created).UTC()

	return view, err
}
