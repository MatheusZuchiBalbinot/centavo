package application

import (
	"context"
	"errors"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

const (
	DefaultStatementLimit = 50
	MaxStatementLimit     = 200
)

var ErrInvalidStatementQuery = errors.New("after_seq must be zero or positive and limit between 1 and 200")

type AccountView struct {
	Account ledger.Account
	Balance money.Balance
}

type OpenAccount struct {
	customers CustomerCreator
}

func NewOpenAccount(customers CustomerCreator) OpenAccount {
	return OpenAccount{customers: customers}
}

// The naive bank ignores the idempotency key, so a retried request opens a
// second, empty account.
func (o OpenAccount) Execute(ctx context.Context, rawHolderName string) (AccountView, error) {
	return o.Apply(ctx, o.customers, rawHolderName)
}

// The writer binds the creator to the unit of work of the idempotency key.
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
