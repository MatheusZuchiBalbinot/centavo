package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
)

// AccountGetter reads one customer account.
type AccountGetter interface {
	Execute(ctx context.Context, id ledger.AccountID) (application.AccountView, error)
}

// StatementGetter pages through an account's entries.
type StatementGetter interface {
	Execute(ctx context.Context, query application.StatementQuery) (application.StatementPage, error)
}

// AccountOpener opens customer accounts inside a unit of work.
type AccountOpener interface {
	Apply(ctx context.Context, customers application.CustomerCreator, holderName string) (application.AccountView, error)
}

// Depositor runs direct deposits inside a unit of work.
type Depositor interface {
	Apply(ctx context.Context, stores application.MoneyStores, operationID ledger.OperationID, input application.DepositInput) (application.DepositResult, error)
}

// Transferrer runs internal transfers inside a unit of work.
type Transferrer interface {
	Apply(ctx context.Context, stores application.MoneyStores, operationID ledger.OperationID, input application.TransferInput) (application.TransferResult, error)
}

// WriteExecutor runs a client write exactly once per idempotency key
// (idempotency) or every time (naive).
type WriteExecutor interface {
	Execute(ctx context.Context, write application.Write) (idempotency.StoredResponse, error)
}

// UseCases are the application entry points the API exposes.
type UseCases struct {
	Writer       WriteExecutor
	Resolver     application.Resolver
	OpenAccount  AccountOpener
	GetAccount   AccountGetter
	GetStatement StatementGetter
	Deposit      Depositor
	Transfer     Transferrer
	Pix          PixUseCases
}

// NewAPIHandler serves the client API (8080).
func NewAPIHandler(useCases UseCases, logger *slog.Logger) http.Handler {
	api := apiHandlers{useCases: useCases, logger: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("POST "+accountsEndpoint, api.openAccount)
	mux.HandleFunc("GET "+accountEndpoint, api.getAccount)
	mux.HandleFunc("GET "+statementEndpoint, api.getStatement)
	mux.HandleFunc("POST "+depositsEndpoint, api.deposit)
	mux.HandleFunc("POST "+transfersEndpoint, api.transfer)
	mux.HandleFunc("POST "+resolveEndpoint, api.resolve)
	mux.HandleFunc("POST "+pixOutEndpoint, api.pixOut)
	mux.HandleFunc("GET "+externalTransferEndpoint, api.getPixOut)
	mux.HandleFunc("POST "+partnerWebhookEndpoint, api.webhook)

	return withSecurityHeaders(withRequestContext(mux, logger))
}

type apiHandlers struct {
	useCases UseCases
	logger   *slog.Logger
}

type accountResponse struct {
	AccountID    string    `json:"account_id"`
	HolderName   string    `json:"holder_name"`
	BalanceCents int64     `json:"balance_cents"`
	CreatedAt    time.Time `json:"created_at"`
}

func (a apiHandlers) getAccount(w http.ResponseWriter, r *http.Request) {
	id, err := ledger.ParseAccountID(r.PathValue("id"))
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	view, err := a.useCases.GetAccount.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	writeJSON(w, http.StatusOK, newAccountResponse(view))
}

type statementResponse struct {
	AccountID    string                  `json:"account_id"`
	Entries      []statementLineResponse `json:"entries"`
	NextAfterSeq *int64                  `json:"next_after_seq"`
}

type statementLineResponse struct {
	AccountSeq        int64     `json:"account_seq"`
	TransactionID     string    `json:"transaction_id"`
	Kind              string    `json:"kind"`
	AmountCents       int64     `json:"amount_cents"`
	BalanceAfterCents int64     `json:"balance_after_cents"`
	CreatedAt         time.Time `json:"created_at"`
}

func (a apiHandlers) getStatement(w http.ResponseWriter, r *http.Request) {
	query, err := statementQueryOf(r)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	page, err := a.useCases.GetStatement.Execute(r.Context(), query)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	writeJSON(w, http.StatusOK, newStatementResponse(query.AccountID, page))
}

func statementQueryOf(r *http.Request) (application.StatementQuery, error) {
	accountID, err := ledger.ParseAccountID(r.PathValue("id"))
	if err != nil {
		return application.StatementQuery{}, err
	}

	values := r.URL.Query()

	afterSeq, err := intParam(values.Get("after_seq"), 0)
	if err != nil {
		return application.StatementQuery{}, err
	}

	limit, err := intParam(values.Get("limit"), application.DefaultStatementLimit)
	if err != nil {
		return application.StatementQuery{}, err
	}

	return application.StatementQuery{AccountID: accountID, AfterSeq: ledger.Seq(afterSeq), Limit: limit}, nil
}

func intParam(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%w: query parameters must be integers", errValidation)
	}

	return value, nil
}

func newAccountResponse(view application.AccountView) accountResponse {
	return accountResponse{
		AccountID: view.Account.ID.String(), HolderName: view.Account.HolderName, BalanceCents: int64(view.Balance.Cents()), CreatedAt: view.Account.CreatedAt.UTC(),
	}
}

func newStatementResponse(accountID ledger.AccountID, page application.StatementPage) statementResponse {
	entries := make([]statementLineResponse, 0, len(page.Lines))

	for _, line := range page.Lines {
		entries = append(entries, newStatementLineResponse(line))
	}

	response := statementResponse{AccountID: accountID.String(), Entries: entries}

	if page.NextAfterSeq != nil {
		next := int64(*page.NextAfterSeq)
		response.NextAfterSeq = &next
	}

	return response
}

func newStatementLineResponse(line ledger.StatementLine) statementLineResponse {
	return statementLineResponse{
		AccountSeq: int64(line.Seq), TransactionID: line.TransactionID.String(), Kind: string(line.TransactionKind),
		AmountCents: int64(line.Amount), BalanceAfterCents: int64(line.BalanceAfter.Cents()), CreatedAt: line.CreatedAt.UTC(),
	}
}
