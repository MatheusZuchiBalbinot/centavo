package application

import (
	"context"

	"centavo/bank/internal/domain/ledger"
)

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
