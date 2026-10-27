package application

import (
	"context"
	"time"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/platform/failpoint"
)

type TransferInput struct {
	From   ledger.AccountID
	To     ledger.AccountID
	Amount money.Amount
	Write  WriteContext
}

type TransferResult struct {
	TransactionID ledger.TransactionID
	FromBalance   money.Balance
	CreatedAt     time.Time
}

type TransferInternal struct {
	unitOfWork UnitOfWork
	locker     Locker
}

func NewTransferInternal(deps MoneyDependencies) TransferInternal {
	return TransferInternal{unitOfWork: deps.UnitOfWork, locker: deps.Locker}
}

// Without balance_locking the source balance is read unlocked and
// overwritten with the value computed here, so two concurrent debits can
// both pass the check and one of them is lost; without atomic_ledger a crash
// at `transfer.after_debit_before_credit` leaves the debit without its
// credit.
func (t TransferInternal) Execute(ctx context.Context, input TransferInput) (TransferResult, error) {
	operationID, err := ledger.NewOperationID()
	if err != nil {
		return TransferResult{}, err
	}

	var result TransferResult

	err = t.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		result, err = t.Apply(ctx, stores, operationID, input)

		return err
	})

	return result, err
}

func (t TransferInternal) Apply(ctx context.Context, stores MoneyStores, operationID ledger.OperationID, input TransferInput) (TransferResult, error) {
	err := ledger.CheckTransferAccounts(input.From, input.To)
	if err != nil {
		return TransferResult{}, err
	}

	err = t.locker.Lock(ctx, stores, []ledger.AccountID{input.From, input.To})
	if err != nil {
		return TransferResult{}, err
	}

	source, err := stores.ReadCustomerPosition(ctx, input.From)
	if err != nil {
		return TransferResult{}, err
	}

	_, err = stores.ReadCustomerPosition(ctx, input.To)
	if err != nil {
		return TransferResult{}, err
	}

	debited, err := source.Debit(input.Amount)
	if err != nil {
		return TransferResult{}, err
	}

	transaction, err := newTransaction(ledger.TransactionKindInternalTransfer, input.Write, operationID)
	if err != nil {
		return TransferResult{}, err
	}

	createdAt, err := stores.InsertTransaction(ctx, transaction)
	if err != nil {
		return TransferResult{}, err
	}

	entries := poster{positions: stores, entries: stores}

	line := debitLine{account: input.From, debited: debited, amount: input.Amount}

	err = entries.debit(ctx, transaction.ID, line)
	if err != nil {
		return TransferResult{}, err
	}

	// `transfer.after_debit_before_credit`: the debit is written; with
	// atomic_ledger it is not committed yet, and dying here rolls everything
	// back. Without atomic_ledger it destroys money.
	err = failpoint.Hit(ctx, failpoint.TransferAfterDebitBeforeCredit)
	if err != nil {
		return TransferResult{}, err
	}

	_, err = entries.post(ctx, transaction.ID, input.To, input.Amount.Cents())
	if err != nil {
		return TransferResult{}, err
	}

	return TransferResult{TransactionID: transaction.ID, FromBalance: debited.Balance, CreatedAt: createdAt}, nil
}
