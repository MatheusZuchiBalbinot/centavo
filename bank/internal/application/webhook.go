package application

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

const (
	EventSettled  EventType = "transfer.settled"
	EventRejected EventType = "transfer.rejected"
	EventReturned EventType = "transfer.returned"
	EventIncoming EventType = "incoming.received"
)

// expectedFrom is the status each transfer event speaks for. Reaching another
// status, created to confirmed for one, needs the partner's authoritative
// status, never just the webhook.
var expectedFrom = map[EventType]transfer.Status{
	EventSettled: transfer.StatusSent, EventRejected: transfer.StatusSent, EventReturned: transfer.StatusConfirmed,
}

type EventType string

type PartnerEvent struct {
	ID          string
	Type        EventType
	EndToEndID  transfer.EndToEndID
	AmountCents money.Cents
	Sequence    int
	Reason      string
	AccountID   string
}

// No inbox, no dedup: events apply in the request that carries them.
type NaiveWebhook struct {
	unitOfWork UnitOfWork
	events     eventApplier
}

type eventApplier struct {
	machine StateMachine
}

type eventStores interface {
	TransitionStores
	creditStores
}

type creditStores interface {
	PixStore
	PositionStore
	LedgerWriter
}

func NewNaiveWebhook(unitOfWork UnitOfWork, machine StateMachine) NaiveWebhook {
	return NaiveWebhook{unitOfWork: unitOfWork, events: eventApplier{machine: machine}}
}

// With the guarded machine, an event that does not fit the transfer is
// refused, and the partner delivers it again later.
func (w NaiveWebhook) Apply(ctx context.Context, event PartnerEvent) error {
	return w.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		return w.events.apply(ctx, stores, event)
	})
}

func (a eventApplier) apply(ctx context.Context, stores eventStores, event PartnerEvent) error {
	switch event.Type {
	case EventSettled:
		return a.applyTransferEvent(ctx, stores, event, transfer.StatusConfirmed)
	case EventRejected:
		return a.applyTransferEvent(ctx, stores, event, transfer.StatusFailed)
	case EventReturned:
		return a.applyTransferEvent(ctx, stores, event, transfer.StatusReversed)
	case EventIncoming:
		return creditIncoming(ctx, stores, event)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownEvent, event.Type)
	}
}

func (a eventApplier) applyTransferEvent(ctx context.Context, stores TransitionStores, event PartnerEvent, to transfer.Status) error {
	external, err := stores.FindExternalTransferByEndToEnd(ctx, event.EndToEndID)
	if err != nil {
		return err
	}

	command := TransitionCommand{TransferID: external.ID, From: expectedFrom[event.Type], To: to, Cause: causeWebhook, CorrelationID: event.ID}

	if to == transfer.StatusFailed {
		command.FailureCode = failureCodeOf(event)
	}

	return a.machine.Move(ctx, stores, command)
}

func failureCodeOf(event PartnerEvent) string {
	if event.Reason == "" {
		return failurePartner
	}

	return event.Reason
}

// The unique end_to_end_id of incoming_transfers keeps it to one credit in
// every profile.
func creditIncoming(ctx context.Context, stores creditStores, event PartnerEvent) error {
	accountID, err := ledger.ParseAccountID(event.AccountID)
	if err != nil {
		return err
	}

	// The PIX before any balance, the order the reconciler takes it in too.
	err = stores.LockEndToEnd(ctx, event.EndToEndID)
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

	postings := []posting{{account: accountID, delta: amount.Cents()}, {account: ledger.PartnerSettlementAccountID(), delta: amount.Negated()}}

	_, err = poster{positions: stores, entries: stores}.postAll(ctx, transactionID, postings)

	return err
}
