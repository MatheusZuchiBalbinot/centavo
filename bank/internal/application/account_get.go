package application

import (
	"context"

	"centavo/bank/internal/domain/ledger"
)

// GetAccount reads a customer account and its balance.
type GetAccount struct {
	customers CustomerReader
}

// NewGetAccount builds the use case.
func NewGetAccount(customers CustomerReader) GetAccount {
	return GetAccount{customers: customers}
}

// Execute returns ledger.ErrAccountNotFound for unknown and system accounts.
func (g GetAccount) Execute(ctx context.Context, id ledger.AccountID) (AccountView, error) {
	account, balance, err := g.customers.FindCustomer(ctx, id)
	if err != nil {
		return AccountView{}, err
	}

	return AccountView{Account: account, Balance: balance}, nil
}
