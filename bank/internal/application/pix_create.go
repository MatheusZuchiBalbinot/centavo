package application

import (
	"context"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

// PixOutInput asks to send money out through the partner.
type PixOutInput struct {
	AccountID      ledger.AccountID
	Amount         money.Amount
	DestinationKey string
	Write          WriteContext
}

// PixOutResult is what the client gets back: the transfer starts as created.
type PixOutResult struct {
	TransferID transfer.ID
	EndToEndID transfer.EndToEndID
	Status     transfer.Status
	CreatedAt  time.Time
}

// CreatePixOut creates the transfer and reserves its amount in the same step,
// so the money is never promised twice.
type CreatePixOut struct {
	locker      Locker
	publication Publication
}

// NewCreatePixOut builds the use case.
func NewCreatePixOut(locker Locker, publication Publication) CreatePixOut {
	return CreatePixOut{locker: locker, publication: publication}
}

// Apply runs inside the writer's unit of work: lock, check funds, move the
// amount to sys_pix_out_transit and record the transfer and its first state.
func (c CreatePixOut) Apply(ctx context.Context, stores Stores, operationID ledger.OperationID, input PixOutInput) (PixOutResult, error) {
	debited, err := c.reserveFunds(ctx, stores, input)
	if err != nil {
		return PixOutResult{}, err
	}

	external, createdAt, err := recordTransfer(ctx, stores, operationID, input)
	if err != nil {
		return PixOutResult{}, err
	}

	transactionID, err := postReserve(ctx, stores, reservation{external: external, debited: debited, write: input.Write})
	if err != nil {
		return PixOutResult{}, err
	}

	first := TransitionRecord{
		TransferID: external.ID, Seq: 1, To: transfer.StatusCreated, Cause: causeAPI, LedgerTransactionID: &transactionID, CorrelationID: input.Write.CorrelationID,
	}

	err = stores.InsertTransition(ctx, first)
	if err != nil {
		return PixOutResult{}, err
	}

	err = c.publication.WithinCommit(ctx, stores, external, input.Write.CorrelationID)
	if err != nil {
		return PixOutResult{}, err
	}

	return PixOutResult{TransferID: external.ID, EndToEndID: external.EndToEndID, Status: transfer.StatusCreated, CreatedAt: createdAt}, nil
}

// reserveFunds locks the customer and transit and returns the customer's
// position after the debit, refusing to take a customer below zero.
func (c CreatePixOut) reserveFunds(ctx context.Context, stores Stores, input PixOutInput) (ledger.Position, error) {
	// Transit is locked with the customer, in id order, so the reserve takes
	// it first, like a refund that moves transit then the customer.
	err := c.locker.Lock(ctx, stores, []ledger.AccountID{input.AccountID, ledger.PixOutTransitAccountID()})
	if err != nil {
		return ledger.Position{}, err
	}

	source, err := stores.ReadCustomerPosition(ctx, input.AccountID)
	if err != nil {
		return ledger.Position{}, err
	}

	return source.Debit(input.Amount)
}

// recordTransfer creates the external transfer in created.
func recordTransfer(ctx context.Context, stores Stores, operationID ledger.OperationID, input PixOutInput) (ExternalTransfer, time.Time, error) {
	id, err := transfer.NewID()
	if err != nil {
		return ExternalTransfer{}, time.Time{}, err
	}

	external := ExternalTransfer{
		ID: id, EndToEndID: id.EndToEndID(), OperationID: operationID, ClientRef: string(input.Write.Key),
		AccountID: input.AccountID, Amount: input.Amount, DestinationKey: input.DestinationKey, Status: transfer.StatusCreated, StatusSeq: 1,
	}

	createdAt, err := stores.InsertExternalTransfer(ctx, external)
	if err != nil {
		return ExternalTransfer{}, time.Time{}, err
	}

	return external, createdAt, nil
}

// reservation is a PIX out whose funds are checked, ready to move to transit.
type reservation struct {
	external ExternalTransfer
	debited  ledger.Position
	write    WriteContext
}

// postReserve writes the reserve: the customer's debit and its twin in
// sys_pix_out_transit, in one ledger transaction tied to the transfer.
func postReserve(ctx context.Context, stores Stores, r reservation) (ledger.TransactionID, error) {
	transaction, err := newTransaction(ledger.TransactionKindPixOutReserve, r.write, r.external.OperationID)
	if err != nil {
		return ledger.TransactionID{}, err
	}

	transferUUID := uuid.UUID(r.external.ID)
	transaction.ExternalTransferID = &transferUUID

	_, err = stores.InsertTransaction(ctx, transaction)
	if err != nil {
		return ledger.TransactionID{}, err
	}

	entries := poster{positions: stores, entries: stores}

	line := debitLine{account: r.external.AccountID, debited: r.debited, amount: r.external.Amount}

	err = entries.debit(ctx, transaction.ID, line)
	if err != nil {
		return ledger.TransactionID{}, err
	}

	_, err = entries.post(ctx, transaction.ID, ledger.PixOutTransitAccountID(), r.external.Amount.Cents())
	if err != nil {
		return ledger.TransactionID{}, err
	}

	return transaction.ID, nil
}
