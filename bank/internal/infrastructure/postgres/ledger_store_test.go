package postgres_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/infrastructure/postgres"
)

const insufficientPrivilege = "42501"

// bank wires the use cases to a real database, as cmd/bank does.
type bank struct {
	pool         *pgxpool.Pool
	openAccount  application.OpenAccount
	getAccount   application.GetAccount
	getStatement application.GetStatement
	deposit      application.Deposit
	transfer     application.TransferInternal
}

func newBank(t *testing.T) bank {
	t.Helper()

	pool := newPool(t, database.appURL)
	store := postgres.NewLedgerStore(pool)
	moneyDependencies := application.MoneyDependencies{Positions: store, Ledger: store}

	return bank{
		pool:         pool,
		openAccount:  application.NewOpenAccount(store),
		getAccount:   application.NewGetAccount(store),
		getStatement: application.NewGetStatement(store, store),
		deposit:      application.NewDeposit(moneyDependencies),
		transfer:     application.NewTransferInternal(moneyDependencies),
	}
}

func (b bank) newCustomer(t *testing.T) ledger.AccountID {
	t.Helper()

	view, err := b.openAccount.Execute(t.Context(), "Test Customer")
	if err != nil {
		t.Fatal(err)
	}

	return view.Account.ID
}

func (b bank) fund(t *testing.T, accountID ledger.AccountID, cents money.Cents) application.DepositResult {
	t.Helper()

	input := application.DepositInput{AccountID: accountID, Amount: amountOf(t, cents), Write: newWrite(t)}

	result, err := b.deposit.Execute(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}

	return result
}

func (b bank) statement(t *testing.T, accountID ledger.AccountID) []ledger.StatementLine {
	t.Helper()

	query := application.StatementQuery{AccountID: accountID, Limit: application.MaxStatementLimit}

	page, err := b.getStatement.Execute(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}

	return page.Lines
}

// transactionsWithClientRef counts the ledger transactions a client key produced.
func (b bank) transactionsWithClientRef(t *testing.T, key idempotency.Key) int {
	t.Helper()

	var count int

	err := b.pool.QueryRow(t.Context(), `SELECT count(*) FROM ledger_transactions WHERE client_ref = $1`, string(key)).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}

	return count
}

func (b bank) entrySum(t *testing.T, transactionID ledger.TransactionID) (sum int64, count int) {
	t.Helper()

	query := `SELECT coalesce(sum(amount_cents), 0), count(*) FROM ledger_entries WHERE transaction_id = $1`

	err := b.pool.QueryRow(t.Context(), query, uuid.UUID(transactionID)).Scan(&sum, &count)
	if err != nil {
		t.Fatal(err)
	}

	return sum, count
}

func TestDeposit_CreditsTheCustomerAndDebitsExternalCash(t *testing.T) {
	b := newBank(t)
	accountID := b.newCustomer(t)
	write := newWrite(t)
	input := application.DepositInput{AccountID: accountID, Amount: amountOf(t, 1_000), Write: write}

	result, err := b.deposit.Execute(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}

	if result.Balance.Cents() != 1_000 {
		t.Fatalf("balance after deposit: got %d, want 1000", result.Balance.Cents())
	}

	sum, count := b.entrySum(t, result.TransactionID)
	if sum != 0 || count != 2 {
		t.Fatalf("deposit entries: sum %d over %d entries, want 0 over 2", sum, count)
	}

	if got := b.transactionsWithClientRef(t, write.Key); got != 1 {
		t.Fatalf("transactions with client_ref %s: got %d, want 1", write.Key, got)
	}

	lines := b.statement(t, accountID)
	wantLine := ledger.StatementLine{Seq: 1, TransactionID: result.TransactionID, TransactionKind: ledger.TransactionKindDeposit, Amount: 1_000, BalanceAfter: money.NewBalance(1_000)}
	assertStatement(t, lines, []ledger.StatementLine{wantLine})
}

func TestTransfer_MovesMoneyAndChainsBalances(t *testing.T) {
	b := newBank(t)
	source := b.newCustomer(t)
	destination := b.newCustomer(t)
	funding := b.fund(t, source, 5_000)
	input := application.TransferInput{From: source, To: destination, Amount: amountOf(t, 1_250), Write: newWrite(t)}

	result, err := b.transfer.Execute(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}

	if result.FromBalance.Cents() != 3_750 {
		t.Fatalf("source balance: got %d, want 3750", result.FromBalance.Cents())
	}

	sum, count := b.entrySum(t, result.TransactionID)
	if sum != 0 || count != 2 {
		t.Fatalf("transfer entries: sum %d over %d entries, want 0 over 2", sum, count)
	}

	wantSource := []ledger.StatementLine{
		{Seq: 1, TransactionID: funding.TransactionID, TransactionKind: ledger.TransactionKindDeposit, Amount: 5_000, BalanceAfter: money.NewBalance(5_000)},
		{Seq: 2, TransactionID: result.TransactionID, TransactionKind: ledger.TransactionKindInternalTransfer, Amount: -1_250, BalanceAfter: money.NewBalance(3_750)},
	}
	assertStatement(t, b.statement(t, source), wantSource)

	wantDestination := []ledger.StatementLine{
		{Seq: 1, TransactionID: result.TransactionID, TransactionKind: ledger.TransactionKindInternalTransfer, Amount: 1_250, BalanceAfter: money.NewBalance(1_250)},
	}
	assertStatement(t, b.statement(t, destination), wantDestination)

	view, err := b.getAccount.Execute(t.Context(), destination)
	if err != nil {
		t.Fatal(err)
	}

	if view.Balance.Cents() != 1_250 {
		t.Fatalf("materialized destination balance: got %d, want 1250", view.Balance.Cents())
	}
}

func TestTransfer_RejectedRequestsWriteNothing(t *testing.T) {
	b := newBank(t)
	source := b.newCustomer(t)
	destination := b.newCustomer(t)
	b.fund(t, source, 100)

	unknown, err := ledger.NewAccountID()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		to    ledger.AccountID
		cents money.Cents
		want  error
	}{
		{name: "insufficient funds", to: destination, cents: 101, want: ledger.ErrInsufficientFunds},
		{name: "unknown destination", to: unknown, cents: 10, want: ledger.ErrAccountNotFound},
		{name: "external cash is not a customer", to: ledger.ExternalCashAccountID(), cents: 10, want: ledger.ErrAccountNotFound},
		{name: "same account", to: source, cents: 10, want: ledger.ErrSameAccount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			write := newWrite(t)
			input := application.TransferInput{From: source, To: tc.to, Amount: amountOf(t, tc.cents), Write: write}

			_, err := b.transfer.Execute(t.Context(), input)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}

			if got := b.transactionsWithClientRef(t, write.Key); got != 0 {
				t.Fatalf("a rejected transfer left %d ledger transactions", got)
			}
		})
	}

	if got := len(b.statement(t, source)); got != 1 {
		t.Fatalf("source has %d entries after rejected transfers, want only the funding", got)
	}
}

func TestStatement_PagesByAccountSeq(t *testing.T) {
	b := newBank(t)
	accountID := b.newCustomer(t)

	for range 3 {
		b.fund(t, accountID, 10)
	}

	first, err := b.getStatement.Execute(t.Context(), application.StatementQuery{AccountID: accountID, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}

	isFirstPageRight := len(first.Lines) == 2 && first.NextAfterSeq != nil && *first.NextAfterSeq == 2
	if !isFirstPageRight {
		t.Fatalf("first page: %d lines, next %v", len(first.Lines), first.NextAfterSeq)
	}

	last, err := b.getStatement.Execute(t.Context(), application.StatementQuery{AccountID: accountID, AfterSeq: 2, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}

	isLastPageRight := len(last.Lines) == 1 && last.Lines[0].Seq == 3 && last.NextAfterSeq == nil
	if !isLastPageRight {
		t.Fatalf("last page: %+v, next %v", last.Lines, last.NextAfterSeq)
	}
}

func TestGetAccount_HidesSystemAccounts(t *testing.T) {
	b := newBank(t)

	_, err := b.getAccount.Execute(t.Context(), ledger.ExternalCashAccountID())
	if !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Fatalf("got %v, want ErrAccountNotFound", err)
	}
}

func TestLedger_IsAppendOnly(t *testing.T) {
	b := newBank(t)
	accountID := b.newCustomer(t)
	b.fund(t, accountID, 10)

	migratorPool := newPool(t, database.migratorURL)

	statements := []struct {
		name string
		pool *pgxpool.Pool
		sql  string
	}{
		{name: "app updates an entry", pool: b.pool, sql: `UPDATE ledger_entries SET amount_cents = amount_cents + 1`},
		{name: "app deletes a transaction", pool: b.pool, sql: `DELETE FROM ledger_transactions`},
		{name: "owner updates an entry", pool: migratorPool, sql: `UPDATE ledger_entries SET amount_cents = amount_cents + 1`},
		{name: "owner deletes a transaction", pool: migratorPool, sql: `DELETE FROM ledger_transactions`},
		{name: "owner truncates the entries", pool: migratorPool, sql: `TRUNCATE ledger_entries`},
	}

	for _, statement := range statements {
		t.Run(statement.name, func(t *testing.T) {
			_, err := statement.pool.Exec(t.Context(), statement.sql)

			var pgErr *pgconn.PgError

			isRefused := errors.As(err, &pgErr) && pgErr.Code == insufficientPrivilege
			if !isRefused {
				t.Fatalf("got %v, want SQLSTATE %s", err, insufficientPrivilege)
			}
		})
	}
}

func assertStatement(t *testing.T, got, want []ledger.StatementLine) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("statement has %d lines, want %d: %+v", len(got), len(want), got)
	}

	for i := range want {
		isSameLine := got[i].Seq == want[i].Seq &&
			got[i].TransactionID == want[i].TransactionID &&
			got[i].TransactionKind == want[i].TransactionKind &&
			got[i].Amount == want[i].Amount &&
			got[i].BalanceAfter == want[i].BalanceAfter
		if !isSameLine {
			t.Fatalf("line %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func amountOf(t *testing.T, cents money.Cents) money.Amount {
	t.Helper()

	amount, err := money.NewAmount(cents)
	if err != nil {
		t.Fatal(err)
	}

	return amount
}

func newWrite(t *testing.T) application.WriteContext {
	t.Helper()

	key := idempotency.Key("test-" + uuid.NewString())

	return application.WriteContext{Key: key, CorrelationID: string(key)}
}
