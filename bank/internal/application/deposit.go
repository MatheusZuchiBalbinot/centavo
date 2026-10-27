package application

import (
	"context"
	"time"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

type DepositInput struct {
	AccountID ledger.AccountID
	Amount    money.Amount
	Write     WriteContext
}

type DepositResult struct {
	TransactionID ledger.TransactionID
	Balance       money.Balance
	CreatedAt     time.Time
}

type Deposit struct {
	unitOfWork UnitOfWork
	locker     Locker
}

func NewDeposit(deps MoneyDependencies) Deposit {
	return Deposit{unitOfWork: deps.UnitOfWork, locker: deps.Locker}
}

// sys_external_cash is updated by increment, never locked up front: every
// deposit touches it, and taking it last keeps it out of any lock cycle.
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

// The writer opens the unit of work so the idempotency key shares its
// commit.
func (d Deposit) Apply(ctx context.Context, stores MoneyStores, operationID ledger.OperationID, input DepositInput) (DepositResult, error) {
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
