package postgres

import (
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

const (
	externalTransfersTable   = "external_transfers"
	transfersTransitionTable = "external_transfer_transitions"
	incomingTransfersTable   = "incoming_transfers"
)

// externalTransferRow is a row of external_transfers.
type externalTransferRow struct {
	ID             uuid.UUID `db:"id"`
	EndToEndID     string    `db:"end_to_end_id"`
	OperationID    uuid.UUID `db:"operation_id"`
	ClientRef      string    `db:"client_ref"`
	AccountID      uuid.UUID `db:"account_id"`
	AmountCents    int64     `db:"amount_cents"`
	DestinationKey string    `db:"destination_key"`
	Status         string    `db:"status"          read:"status::text"`
	StatusSeq      int       `db:"status_seq"`
	FailureCode    string    `db:"failure_code"    read:"coalesce(failure_code, '')"`
	CreatedAt      time.Time `db:"created_at"`
}

// newExternalTransferRow is what a new transfer writes: created_at and the
// failure code come from the database.
type newExternalTransferRow struct {
	ID             uuid.UUID `db:"id"`
	EndToEndID     string    `db:"end_to_end_id"`
	OperationID    uuid.UUID `db:"operation_id"`
	ClientRef      string    `db:"client_ref"`
	AccountID      uuid.UUID `db:"account_id"`
	AmountCents    int64     `db:"amount_cents"`
	DestinationKey string    `db:"destination_key"`
	Status         string    `db:"status"`
	StatusSeq      int       `db:"status_seq"`
}

// transitionRow is a row of external_transfer_transitions.
type transitionRow struct {
	TransferID          uuid.UUID  `db:"transfer_id"`
	Seq                 int        `db:"seq"`
	FromStatus          *string    `db:"from_status"           read:"from_status::text"`
	ToStatus            string     `db:"to_status"             read:"to_status::text"`
	Cause               string     `db:"cause"`
	LedgerTransactionID *uuid.UUID `db:"ledger_transaction_id"`
	CorrelationID       string     `db:"correlation_id"`
}

// transitionReadRow is a transition as read back, with its time.
type transitionReadRow struct {
	transitionRow

	CreatedAt time.Time `db:"created_at"`
}

// incomingTransferRow is a row of incoming_transfers.
type incomingTransferRow struct {
	ID            uuid.UUID `db:"id"`
	EndToEndID    string    `db:"end_to_end_id"`
	AccountID     uuid.UUID `db:"account_id"`
	AmountCents   int64     `db:"amount_cents"`
	SourceEventID string    `db:"source_event_id"`
}

func newExternalTransferRowOf(external application.ExternalTransfer) newExternalTransferRow {
	return newExternalTransferRow{
		ID: uuid.UUID(external.ID), EndToEndID: string(external.EndToEndID), OperationID: uuid.UUID(external.OperationID),
		ClientRef: external.ClientRef, AccountID: uuid.UUID(external.AccountID), AmountCents: int64(external.Amount.Cents()),
		DestinationKey: external.DestinationKey, Status: string(transfer.StatusCreated), StatusSeq: 1,
	}
}

func (row externalTransferRow) toDomain() (application.ExternalTransfer, error) {
	amount, err := money.NewAmount(money.Cents(row.AmountCents))
	if err != nil {
		return application.ExternalTransfer{}, err
	}

	return application.ExternalTransfer{
		ID: transfer.ID(row.ID), EndToEndID: transfer.EndToEndID(row.EndToEndID), OperationID: ledger.OperationID(row.OperationID),
		ClientRef: row.ClientRef, AccountID: ledger.AccountID(row.AccountID), Amount: amount,
		DestinationKey: row.DestinationKey, Status: transfer.Status(row.Status), StatusSeq: row.StatusSeq,
		FailureCode: row.FailureCode, CreatedAt: row.CreatedAt,
	}, nil
}

func transitionRowOf(transition application.TransitionRecord) transitionRow {
	row := transitionRow{
		TransferID: uuid.UUID(transition.TransferID), Seq: transition.Seq, ToStatus: string(transition.To),
		Cause: string(transition.Cause), CorrelationID: transition.CorrelationID,
	}

	if transition.From != nil {
		from := string(*transition.From)
		row.FromStatus = &from
	}

	if transition.LedgerTransactionID != nil {
		id := uuid.UUID(*transition.LedgerTransactionID)
		row.LedgerTransactionID = &id
	}

	return row
}

func (row transitionReadRow) toDomain() application.TransitionRecord {
	transition := application.TransitionRecord{
		TransferID: transfer.ID(row.TransferID), Seq: row.Seq, To: transfer.Status(row.ToStatus),
		Cause: application.TransitionCause(row.Cause), CorrelationID: row.CorrelationID, CreatedAt: row.CreatedAt,
	}

	if row.FromStatus != nil {
		from := transfer.Status(*row.FromStatus)
		transition.From = &from
	}

	if row.LedgerTransactionID != nil {
		id := ledger.TransactionID(*row.LedgerTransactionID)
		transition.LedgerTransactionID = &id
	}

	return transition
}

func incomingTransferRowOf(incoming application.IncomingRecord) incomingTransferRow {
	return incomingTransferRow{
		ID: incoming.ID, EndToEndID: string(incoming.EndToEndID), AccountID: uuid.UUID(incoming.AccountID),
		AmountCents: int64(incoming.Amount.Cents()), SourceEventID: incoming.SourceEventID,
	}
}
