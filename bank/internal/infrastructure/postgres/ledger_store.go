package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

const customerFloorCents = 0

// One statement, so neither the account nor its balance exists without the
// other.
const createCustomerSQL = `
WITH account AS (
    INSERT INTO accounts (id, kind, holder_name)
    VALUES (@id, 'customer', @holder_name)
    RETURNING id, created_at
), balance AS (
    INSERT INTO account_balances (account_id, min_balance_cents)
    SELECT id, @min_balance_cents FROM account
)
SELECT created_at FROM account`

// Every method is a single statement; whether statements commit together
// depends on the querier it was built on.
type LedgerStore struct {
	ledgerRows
	keyStore
	pixStore
	outboxStore
	inboxStore
	leaseStore
	reconciliationStore
}

// On a pool every statement is its own autocommit; inside a transaction
// they commit together.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type ledgerRows struct {
	db querier
}

func NewLedgerStore(pool *pgxpool.Pool) LedgerStore {
	return storesOn(pool)
}

func storesOn(db querier) LedgerStore {
	return LedgerStore{
		ledgerRows: ledgerRows{db: db}, keyStore: keyStore{db: db}, pixStore: pixStore{db: db},
		outboxStore: outboxStore{db: db}, inboxStore: inboxStore{db: db}, leaseStore: leaseStore{db: db}, reconciliationStore: reconciliationStore{db: db},
	}
}

// In id order, so two transfers in opposite directions never wait for each
// other in a cycle. The locks last until the transaction ends.
func (s ledgerRows) LockPositions(ctx context.Context, ids []ledger.AccountID) error {
	uuids := make([]uuid.UUID, len(ids))

	for k, id := range ids {
		uuids[k] = uuid.UUID(id)
	}

	_, err := selectFrom[accountIDRow]("locking positions", accountBalancesTable).
		Where("account_id = ANY(@ids)", args{"ids": uuids}).
		OrderBy("account_id").
		ForUpdate().
		All(ctx, s.db)

	return err
}

func (s ledgerRows) CreateCustomer(ctx context.Context, account ledger.Account) (time.Time, error) {
	var createdAt time.Time

	values := args{"id": uuid.UUID(account.ID), "holder_name": account.HolderName, "min_balance_cents": customerFloorCents}
	err := scanRow(ctx, s.db, newStatement("creating customer account", createCustomerSQL, values), &createdAt)

	return createdAt, err
}

func (s ledgerRows) FindCustomer(ctx context.Context, id ledger.AccountID) (ledger.Account, money.Balance, error) {
	row, err := selectFrom[customerRow]("reading customer account", customersWithBalances).
		Where("a.id = @id", args{"id": uuid.UUID(id)}).
		One(ctx, s.db)
	if err != nil {
		return ledger.Account{}, money.Balance{}, accountNotFoundOr(err)
	}

	account := ledger.Account{ID: id, Kind: ledger.AccountKindCustomer, HolderName: row.HolderName, CreatedAt: row.CreatedAt}

	return account, money.NewBalance(money.Cents(row.BalanceCents)), nil
}

// Without locking the row.
func (s ledgerRows) ReadCustomerPosition(ctx context.Context, id ledger.AccountID) (ledger.Position, error) {
	row, err := selectFrom[positionRow]("reading customer position", customersWithBalances).
		Where("b.account_id = @id", args{"id": uuid.UUID(id)}).
		One(ctx, s.db)
	if err != nil {
		return ledger.Position{}, accountNotFoundOr(err)
	}

	return row.toDomain(), nil
}

func (s ledgerRows) OverwritePosition(ctx context.Context, id ledger.AccountID, position ledger.Position) error {
	return update("overwriting position", accountBalancesTable).
		Set("balance_cents", int64(position.Balance.Cents())).
		Set("last_entry_seq", int64(position.LastSeq)).
		SetSQL("updated_at = now()", nil).
		Where("account_id = @id", args{"id": uuid.UUID(id)}).
		ExecOne(ctx, s.db, ledger.ErrAccountNotFound)
}

func (s ledgerRows) IncrementBalance(ctx context.Context, id ledger.AccountID, delta money.Cents) (ledger.Position, error) {
	var row positionRow

	err := update("incrementing balance", accountBalancesTable).
		SetSQL("balance_cents = balance_cents + @delta", args{"delta": int64(delta)}).
		SetSQL("last_entry_seq = last_entry_seq + 1, updated_at = now()", nil).
		Where("account_id = @id", args{"id": uuid.UUID(id)}).
		Returning("balance_cents", "last_entry_seq").
		Scan(ctx, s.db, &row.BalanceCents, &row.LastEntrySeq)
	if err != nil {
		return ledger.Position{}, accountNotFoundOr(err)
	}

	return row.toDomain(), nil
}

func (s ledgerRows) InsertTransaction(ctx context.Context, transaction ledger.Transaction) (time.Time, error) {
	var createdAt time.Time

	insert := insertRow("inserting ledger transaction", ledgerTransactionsTable, ledgerTransactionRowOf(transaction))
	err := insert.Returning("created_at").Scan(ctx, s.db, &createdAt)

	return createdAt, err
}

func (s ledgerRows) InsertEntry(ctx context.Context, entry ledger.Entry) error {
	_, err := insertRow("inserting ledger entry", ledgerEntriesTable, ledgerEntryRowOf(entry)).Exec(ctx, s.db)

	return err
}

// The id breaks ties: without the balance_locking defense, two entries can
// share a position.
func (s ledgerRows) ReadStatement(ctx context.Context, query application.StatementQuery) ([]ledger.StatementLine, error) {
	rows, err := selectFrom[statementLineRow]("reading statement", entriesWithTransactions).
		Where("e.account_id = @account_id AND e.account_seq > @after_seq", args{"account_id": uuid.UUID(query.AccountID), "after_seq": int64(query.AfterSeq)}).
		OrderBy("e.account_seq, e.id").
		Limit(query.Limit).
		All(ctx, s.db)
	if err != nil {
		return nil, err
	}

	lines := make([]ledger.StatementLine, len(rows))

	for k, row := range rows {
		lines[k] = row.toDomain()
	}

	return lines, nil
}

func accountNotFoundOr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ledger.ErrAccountNotFound
	}

	return err
}
