package application

import (
	"context"
	"time"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/platform/failpoint"
)

// TransferInput asks to move money between two customer accounts.
type TransferInput struct {
	From   ledger.AccountID
	To     ledger.AccountID
	Amount money.Amount
	Write  WriteContext
}

// TransferResult is what the client gets back from an internal transfer.
type TransferResult struct {
	TransactionID ledger.TransactionID
	FromBalance   money.Balance
	CreatedAt     time.Time
}

// TransferInternal moves money between two customers (docs/domain.md, section 6).
type TransferInternal struct {
	positions PositionStore
	ledger    LedgerWriter
	poster    poster
}

// NewTransferInternal builds the use case.
func NewTransferInternal(deps MoneyDependencies) TransferInternal {
	return TransferInternal{positions: deps.Positions, ledger: deps.Ledger, poster: poster{positions: deps.Positions, entries: deps.Ledger}}
}

// Execute runs the naive transfer of docs/architecture.md, section 5.2: the
// source balance is read without a lock, checked and overwritten with the
// value computed here, so two concurrent debits can both pass the check and
// one of them is lost.
func (t TransferInternal) Execute(ctx context.Context, input TransferInput) (TransferResult, error) {
	err := ledger.CheckTransferAccounts(input.From, input.To)
	if err != nil {
		return TransferResult{}, err
	}

	source, err := t.positions.ReadCustomerPosition(ctx, input.From)
	if err != nil {
		return TransferResult{}, err
	}

	_, err = t.positions.ReadCustomerPosition(ctx, input.To)
	if err != nil {
		return TransferResult{}, err
	}

	debited, err := source.Debit(input.Amount)
	if err != nil {
		return TransferResult{}, err
	}

	transaction, err := newTransaction(ledger.TransactionKindInternalTransfer, input.Write)
	if err != nil {
		return TransferResult{}, err
	}

	createdAt, err := t.ledger.InsertTransaction(ctx, transaction)
	if err != nil {
		return TransferResult{}, err
	}

	err = t.positions.OverwritePosition(ctx, input.From, debited)
	if err != nil {
		return TransferResult{}, err
	}

	debit := ledger.Entry{TransactionID: transaction.ID, AccountID: input.From, Amount: input.Amount.Negated(), Seq: debited.LastSeq, BalanceAfter: debited.Balance}

	err = t.ledger.InsertEntry(ctx, debit)
	if err != nil {
		return TransferResult{}, err
	}

	// FP01: the debit is already written; dying here destroys money (FC01).
	err = failpoint.Hit(ctx, failpoint.TransferAfterDebitBeforeCredit)
	if err != nil {
		return TransferResult{}, err
	}

	_, err = t.poster.post(ctx, transaction.ID, input.To, input.Amount.Cents())
	if err != nil {
		return TransferResult{}, err
	}

	return TransferResult{TransactionID: transaction.ID, FromBalance: debited.Balance, CreatedAt: createdAt}, nil
}
