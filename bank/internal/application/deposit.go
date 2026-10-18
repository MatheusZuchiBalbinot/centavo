package application

import (
	"context"
	"time"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

// DepositInput asks to credit money that arrives from outside the bank.
type DepositInput struct {
	AccountID ledger.AccountID
	Amount    money.Amount
	Write     WriteContext
}

// DepositResult is what the client gets back from a deposit.
type DepositResult struct {
	TransactionID ledger.TransactionID
	Balance       money.Balance
	CreatedAt     time.Time
}

// Deposit credits a customer and debits sys_external_cash (docs/domain.md,
// section 7).
type Deposit struct {
	unitOfWork UnitOfWork
	locker     Locker
}

// NewDeposit builds the use case.
func NewDeposit(deps MoneyDependencies) Deposit {
	return Deposit{unitOfWork: deps.UnitOfWork, locker: deps.Locker}
}

// Execute locks the customer's balance (D2), then writes the transaction and
// its two entries in one unit of work (D1). sys_external_cash is updated by
// increment, never locked up front: every deposit touches it, and taking it
// last keeps it out of any lock cycle.
func (d Deposit) Execute(ctx context.Context, input DepositInput) (DepositResult, error) {
	operationID, err := ledger.NewOperationID()
	if err != nil {
		return DepositResult{}, err
	}

	var result DepositResult

	err = d.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		result, err = d.Apply(ctx, stores, operationID, input)

		return err
	})

	return result, err
}

// Apply runs the deposit inside a unit of work that is already open; the
// writer opens it so the idempotency key shares its commit.
func (d Deposit) Apply(ctx context.Context, stores Stores, operationID ledger.OperationID, input DepositInput) (DepositResult, error) {
	err := d.locker.Lock(ctx, stores, []ledger.AccountID{input.AccountID})
	if err != nil {
		return DepositResult{}, err
	}

	_, err = stores.ReadCustomerPosition(ctx, input.AccountID)
	if err != nil {
		return DepositResult{}, err
	}

	transaction, err := newTransaction(ledger.TransactionKindDeposit, input.Write, operationID)
	if err != nil {
		return DepositResult{}, err
	}

	createdAt, err := stores.InsertTransaction(ctx, transaction)
	if err != nil {
		return DepositResult{}, err
	}

	postings := []posting{{account: input.AccountID, delta: input.Amount.Cents()}, {account: ledger.ExternalCashAccountID(), delta: input.Amount.Negated()}}

	positions, err := poster{positions: stores, entries: stores}.postAll(ctx, transaction.ID, postings)
	if err != nil {
		return DepositResult{}, err
	}

	return DepositResult{TransactionID: transaction.ID, Balance: positions[input.AccountID].Balance, CreatedAt: createdAt}, nil
}
