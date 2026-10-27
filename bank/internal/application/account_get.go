package application

import (
	"context"

	"centavo/bank/internal/domain/ledger"
)

type GetAccount struct {
	customers CustomerReader
}

func NewGetAccount(customers CustomerReader) GetAccount {
	return GetAccount{customers: customers}
}

// ledger.ErrAccountNotFound for unknown and system accounts.
func (g GetAccount) Execute(ctx context.Context, id ledger.AccountID) (AccountView, error) {
	account, balance, err := g.customers.FindCustomer(ctx, id)
	if err != nil {
		return AccountView{}, err
	}

	return AccountView{Account: account, Balance: balance}, nil
}
