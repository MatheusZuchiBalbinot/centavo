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

// StatementQuery asks for the entries after a position, keyset style.
type StatementQuery struct {
	AccountID ledger.AccountID
	AfterSeq  ledger.Seq
	Limit     int
}

// StatementPage is one page of a statement. NextAfterSeq is nil on the last page.
type StatementPage struct {
	Lines        []ledger.StatementLine
	NextAfterSeq *ledger.Seq
}

// GetStatement pages through a customer account's entries.
type GetStatement struct {
	customers  CustomerReader
	statements StatementReader
}

// NewGetStatement builds the use case.
func NewGetStatement(customers CustomerReader, statements StatementReader) GetStatement {
	return GetStatement{customers: customers, statements: statements}
}

// Execute reads one page; an unknown account is ledger.ErrAccountNotFound,
// never an empty page.
func (g GetStatement) Execute(ctx context.Context, query StatementQuery) (StatementPage, error) {
	isValidQuery := query.AfterSeq >= 0 && query.Limit >= 1 && query.Limit <= MaxStatementLimit
	if !isValidQuery {
		return StatementPage{}, ErrInvalidStatementQuery
	}

	_, _, err := g.customers.FindCustomer(ctx, query.AccountID)
	if err != nil {
		return StatementPage{}, err
	}

	lines, err := g.statements.ReadStatement(ctx, query)
	if err != nil {
		return StatementPage{}, err
	}

	return StatementPage{Lines: lines, NextAfterSeq: nextAfterSeq(lines, query.Limit)}, nil
}

// nextAfterSeq points at the last line of a full page; a short page is the last one.
func nextAfterSeq(lines []ledger.StatementLine, limit int) *ledger.Seq {
	isFullPage := len(lines) == limit
	if !isFullPage {
		return nil
	}

	last := lines[len(lines)-1].Seq

	return &last
}
