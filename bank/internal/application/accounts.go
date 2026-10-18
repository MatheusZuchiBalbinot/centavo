package application

import (
	"context"
	"errors"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

const (
	// DefaultStatementLimit is the page size when the client asks for none.
	DefaultStatementLimit = 50
	// MaxStatementLimit caps one statement page.
	MaxStatementLimit = 200
)

// ErrInvalidStatementQuery means a statement page asked for a cursor or a size
// out of bounds.
var ErrInvalidStatementQuery = errors.New("after_seq must be zero or positive and limit between 1 and 200")

// AccountView is a customer account and its balance.
type AccountView struct {
	Account ledger.Account
	Balance money.Balance
}

// OpenAccount creates a customer account with a zero balance.
type OpenAccount struct {
	customers CustomerCreator
}

// NewOpenAccount builds the use case.
func NewOpenAccount(customers CustomerCreator) OpenAccount {
	return OpenAccount{customers: customers}
}

// Execute creates the account. The naive bank ignores the idempotency key,
// so a retried request opens a second, empty account.
func (o OpenAccount) Execute(ctx context.Context, rawHolderName string) (AccountView, error) {
	return o.Apply(ctx, o.customers, rawHolderName)
}

// Apply opens the account with the given creator, which the writer binds to
// the unit of work of the idempotency key.
func (o OpenAccount) Apply(ctx context.Context, customers CustomerCreator, rawHolderName string) (AccountView, error) {
	holderName, err := ledger.ParseHolderName(rawHolderName)
	if err != nil {
		return AccountView{}, err
	}

	id, err := ledger.NewAccountID()
	if err != nil {
		return AccountView{}, err
	}

	account := ledger.Account{ID: id, Kind: ledger.AccountKindCustomer, HolderName: holderName}

	createdAt, err := customers.CreateCustomer(ctx, account)
	if err != nil {
		return AccountView{}, err
	}

	account.CreatedAt = createdAt

	return AccountView{Account: account, Balance: money.NewBalance(0)}, nil
}
