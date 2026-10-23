package postgres

import (
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

const (
	accountBalancesTable    = "account_balances"
	ledgerTransactionsTable = "ledger_transactions"
	ledgerEntriesTable      = "ledger_entries"

	// customersWithBalances joins a customer account to its balance; the
	// row models read from it name their columns by alias.
	customersWithBalances = "accounts a JOIN account_balances b ON b.account_id = a.id AND a.kind = 'customer'"

	// entriesWithTransactions joins each entry to its transaction's kind.
	entriesWithTransactions = "ledger_entries e JOIN ledger_transactions t ON t.id = e.transaction_id"
)

// accountIDRow reads only an account id.
type accountIDRow struct {
	AccountID uuid.UUID `db:"account_id"`
}

// customerRow is a customer account with its materialized balance.
type customerRow struct {
	HolderName   string    `db:"holder_name"   read:"a.holder_name"`
	CreatedAt    time.Time `db:"created_at"    read:"a.created_at"`
	BalanceCents int64     `db:"balance_cents" read:"b.balance_cents"`
}

// positionRow is a balance and the position of its last entry.
type positionRow struct {
	BalanceCents int64 `db:"balance_cents"  read:"b.balance_cents"`
	LastEntrySeq int64 `db:"last_entry_seq" read:"b.last_entry_seq"`
}

// ledgerTransactionRow is a row of ledger_transactions; an empty client
// reference is stored as NULL.
type ledgerTransactionRow struct {
	ID                   uuid.UUID  `db:"id"`
	Kind                 string     `db:"kind"`
	OperationID          *uuid.UUID `db:"operation_id"`
	ClientRef            *string    `db:"client_ref"`
	CorrelationID        string     `db:"correlation_id"`
	ExternalTransferID   *uuid.UUID `db:"external_transfer_id"`
	IncomingTransferID   *uuid.UUID `db:"incoming_transfer_id"`
	ReconciliationItemID *int64     `db:"reconciliation_item_id"`
}

// ledgerEntryRow is a row of ledger_entries.
type ledgerEntryRow struct {
	TransactionID     uuid.UUID `db:"transaction_id"`
	AccountID         uuid.UUID `db:"account_id"`
	AmountCents       int64     `db:"amount_cents"`
	AccountSeq        int64     `db:"account_seq"`
	BalanceAfterCents int64     `db:"balance_after_cents"`
}

// statementLineRow is an entry with its transaction's kind.
type statementLineRow struct {
	AccountSeq        int64     `db:"account_seq"         read:"e.account_seq"`
	TransactionID     uuid.UUID `db:"transaction_id"      read:"e.transaction_id"`
	Kind              string    `db:"kind"                read:"t.kind"`
	AmountCents       int64     `db:"amount_cents"        read:"e.amount_cents"`
	BalanceAfterCents int64     `db:"balance_after_cents" read:"e.balance_after_cents"`
	CreatedAt         time.Time `db:"created_at"          read:"e.created_at"`
}

func (row positionRow) toDomain() ledger.Position {
	return ledger.Position{Balance: money.NewBalance(money.Cents(row.BalanceCents)), LastSeq: ledger.Seq(row.LastEntrySeq)}
}

func ledgerTransactionRowOf(transaction ledger.Transaction) ledgerTransactionRow {
	row := ledgerTransactionRow{
		ID: uuid.UUID(transaction.ID), Kind: string(transaction.Kind), CorrelationID: transaction.CorrelationID,
		ExternalTransferID: transaction.ExternalTransferID, IncomingTransferID: transaction.IncomingTransferID,
		ReconciliationItemID: transaction.ReconciliationItemID,
	}

	if transaction.OperationID != nil {
		id := uuid.UUID(*transaction.OperationID)
		row.OperationID = &id
	}

	if transaction.ClientRef != "" {
		ref := transaction.ClientRef
		row.ClientRef = &ref
	}

	return row
}

func ledgerEntryRowOf(entry ledger.Entry) ledgerEntryRow {
	return ledgerEntryRow{
		TransactionID: uuid.UUID(entry.TransactionID), AccountID: uuid.UUID(entry.AccountID), AmountCents: int64(entry.Amount),
		AccountSeq: int64(entry.Seq), BalanceAfterCents: int64(entry.BalanceAfter.Cents()),
	}
}

func (row statementLineRow) toDomain() ledger.StatementLine {
	return ledger.StatementLine{
		Seq: ledger.Seq(row.AccountSeq), TransactionID: ledger.TransactionID(row.TransactionID),
		TransactionKind: ledger.TransactionKind(row.Kind), Amount: money.Cents(row.AmountCents),
		BalanceAfter: money.NewBalance(money.Cents(row.BalanceAfterCents)), CreatedAt: row.CreatedAt,
	}
}
