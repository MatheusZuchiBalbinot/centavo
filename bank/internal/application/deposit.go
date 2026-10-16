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
// section 7), one autocommit statement at a time.
type Deposit struct {
	positions PositionReader
	ledger    LedgerWriter
	poster    poster
}

// NewDeposit builds the use case.
func NewDeposit(deps MoneyDependencies) Deposit {
	return Deposit{positions: deps.Positions, ledger: deps.Ledger, poster: poster{positions: deps.Positions, entries: deps.Ledger}}
}

// Execute runs the naive deposit: a crash between two statements leaves a
// transaction with only some of its entries.
func (d Deposit) Execute(ctx context.Context, input DepositInput) (DepositResult, error) {
	_, err := d.positions.ReadCustomerPosition(ctx, input.AccountID)
	if err != nil {
		return DepositResult{}, err
	}

	transaction, err := newTransaction(ledger.TransactionKindDeposit, input.Write)
	if err != nil {
		return DepositResult{}, err
	}

	createdAt, err := d.ledger.InsertTransaction(ctx, transaction)
	if err != nil {
		return DepositResult{}, err
	}

	customer, err := d.poster.post(ctx, transaction.ID, input.AccountID, input.Amount.Cents())
	if err != nil {
		return DepositResult{}, err
	}

	_, err = d.poster.post(ctx, transaction.ID, ledger.ExternalCashAccountID(), input.Amount.Negated())
	if err != nil {
		return DepositResult{}, err
	}

	return DepositResult{TransactionID: transaction.ID, Balance: customer.Balance, CreatedAt: createdAt}, nil
}
