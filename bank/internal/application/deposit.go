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
	var result DepositResult

	err := d.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		result, err = d.deposit(ctx, stores, input)

		return err
	})

	return result, err
}

func (d Deposit) deposit(ctx context.Context, stores Stores, input DepositInput) (DepositResult, error) {
	err := d.locker.Lock(ctx, stores, []ledger.AccountID{input.AccountID})
	if err != nil {
		return DepositResult{}, err
	}

	_, err = stores.ReadCustomerPosition(ctx, input.AccountID)
	if err != nil {
		return DepositResult{}, err
	}

	transaction, err := newTransaction(ledger.TransactionKindDeposit, input.Write)
	if err != nil {
		return DepositResult{}, err
	}

	createdAt, err := stores.InsertTransaction(ctx, transaction)
	if err != nil {
		return DepositResult{}, err
	}

	entries := poster{positions: stores, entries: stores}

	customer, err := entries.post(ctx, transaction.ID, input.AccountID, input.Amount.Cents())
	if err != nil {
		return DepositResult{}, err
	}

	_, err = entries.post(ctx, transaction.ID, ledger.ExternalCashAccountID(), input.Amount.Negated())
	if err != nil {
		return DepositResult{}, err
	}

	return DepositResult{TransactionID: transaction.ID, Balance: customer.Balance, CreatedAt: createdAt}, nil
}
