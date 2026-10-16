package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

// Event types the partner sends (docs/architecture.md, section 4.4).
const (
	EventSettled  = "transfer.settled"
	EventRejected = "transfer.rejected"
	EventReturned = "transfer.returned"
	EventIncoming = "incoming.received"
)

// PartnerEvent is a webhook from the partner.
type PartnerEvent struct {
	ID          string
	Type        string
	EndToEndID  string
	AmountCents money.Cents
	Sequence    int
	Reason      string
	AccountID   string
}

// NaiveWebhook applies partner events as they arrive, in the request that
// carries them (docs/architecture.md, section 5.4): no inbox, no dedup, the
// status overwritten by whatever the event says.
type NaiveWebhook struct {
	unitOfWork UnitOfWork
}

// NewNaiveWebhook builds the handler's use case.
func NewNaiveWebhook(unitOfWork UnitOfWork) NaiveWebhook {
	return NaiveWebhook{unitOfWork: unitOfWork}
}

// Apply runs the event's effect.
func (w NaiveWebhook) Apply(ctx context.Context, event PartnerEvent) error {
	return w.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		return applyEvent(ctx, stores, event)
	})
}

func applyEvent(ctx context.Context, stores Stores, event PartnerEvent) error {
	switch event.Type {
	case EventSettled:
		return applyTransferEvent(ctx, stores, event, StatusChange{To: transfer.StatusConfirmed})
	case EventRejected:
		return applyTransferEvent(ctx, stores, event, StatusChange{To: transfer.StatusFailed, FailureCode: failureCodeOf(event)})
	case EventReturned:
		return applyTransferEvent(ctx, stores, event, StatusChange{To: transfer.StatusReversed})
	case EventIncoming:
		return creditIncoming(ctx, stores, event)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownEvent, event.Type)
	}
}

func applyTransferEvent(ctx context.Context, stores Stores, event PartnerEvent, change StatusChange) error {
	external, err := stores.FindExternalTransferByEndToEnd(ctx, event.EndToEndID)
	if err != nil {
		return err
	}

	change.TransferID = external.ID

	return overwrite(ctx, stores, overwriteCommand{external: external, change: change, cause: causeWebhook, correlationID: event.ID})
}

func failureCodeOf(event PartnerEvent) string {
	if event.Reason == "" {
		return failurePartner
	}

	return event.Reason
}

// creditIncoming credits a PIX in (docs/domain.md, section 9). The unique
// end_to_end_id of incoming_transfers keeps it to one credit in every profile.
func creditIncoming(ctx context.Context, stores Stores, event PartnerEvent) error {
	accountID, err := ledger.ParseAccountID(event.AccountID)
	if err != nil {
		return err
	}

	amount, err := money.NewAmount(event.AmountCents)
	if err != nil {
		return err
	}

	_, err = stores.ReadCustomerPosition(ctx, accountID)
	if err != nil {
		return err
	}

	incomingID, err := uuid.NewV7()
	if err != nil {
		return err
	}

	err = stores.InsertIncoming(ctx, IncomingRecord{ID: incomingID, EndToEndID: event.EndToEndID, AccountID: accountID, Amount: amount, SourceEventID: event.ID})
	if err != nil {
		return err
	}

	transactionID, err := ledger.NewTransactionID()
	if err != nil {
		return err
	}

	transaction := ledger.Transaction{ID: transactionID, Kind: ledger.TransactionKindPixInCredit, CorrelationID: event.ID, IncomingTransferID: &incomingID}

	_, err = stores.InsertTransaction(ctx, transaction)
	if err != nil {
		return err
	}

	writer := poster{positions: stores, entries: stores}

	_, err = writer.post(ctx, transactionID, accountID, amount.Cents())
	if err != nil {
		return err
	}

	_, err = writer.post(ctx, transactionID, ledger.PartnerSettlementAccountID(), amount.Negated())

	return err
}
