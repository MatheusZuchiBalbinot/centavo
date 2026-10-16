package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

const (
	idempotencyHeader = "Idempotency-Key"
	// maxBodyBytes is far above any valid request body of the API.
	maxBodyBytes = 16 << 10
)

// AccountOpener opens customer accounts.
type AccountOpener interface {
	Execute(ctx context.Context, holderName string) (application.AccountView, error)
}

// AccountGetter reads one customer account.
type AccountGetter interface {
	Execute(ctx context.Context, id ledger.AccountID) (application.AccountView, error)
}

// StatementGetter pages through an account's entries.
type StatementGetter interface {
	Execute(ctx context.Context, query application.StatementQuery) (application.StatementPage, error)
}

// Depositor runs direct deposits.
type Depositor interface {
	Execute(ctx context.Context, input application.DepositInput) (application.DepositResult, error)
}

// Transferrer runs internal transfers.
type Transferrer interface {
	Execute(ctx context.Context, input application.TransferInput) (application.TransferResult, error)
}

// UseCases are the application entry points the API exposes.
type UseCases struct {
	OpenAccount  AccountOpener
	GetAccount   AccountGetter
	GetStatement StatementGetter
	Deposit      Depositor
	Transfer     Transferrer
}

// NewAPIHandler serves the client API (8080).
func NewAPIHandler(useCases UseCases, logger *slog.Logger) http.Handler {
	api := apiHandlers{useCases: useCases, logger: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/accounts", api.openAccount)
	mux.HandleFunc("GET /v1/accounts/{id}", api.getAccount)
	mux.HandleFunc("GET /v1/accounts/{id}/statement", api.getStatement)
	mux.HandleFunc("POST /v1/deposits", api.deposit)
	mux.HandleFunc("POST /v1/transfers", api.transfer)

	return withRequestContext(mux, logger)
}

type apiHandlers struct {
	useCases UseCases
	logger   *slog.Logger
}

type openAccountRequest struct {
	HolderName string `json:"holder_name"`
}

type accountResponse struct {
	AccountID    string    `json:"account_id"`
	HolderName   string    `json:"holder_name"`
	BalanceCents int64     `json:"balance_cents"`
	CreatedAt    time.Time `json:"created_at"`
}

func (a apiHandlers) openAccount(w http.ResponseWriter, r *http.Request) {
	// The naive bank requires the key (BR06) but does nothing with it.
	_, err := writeContextOf(r)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	var request openAccountRequest

	err = decodeBody(w, r, &request)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	view, err := a.useCases.OpenAccount.Execute(r.Context(), request.HolderName)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	writeJSON(w, http.StatusCreated, newAccountResponse(view))
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

type depositRequest struct {
	AccountID   string `json:"account_id"`
	AmountCents int64  `json:"amount_cents"`
}

type depositResponse struct {
	TransactionID string    `json:"transaction_id"`
	AccountID     string    `json:"account_id"`
	BalanceCents  int64     `json:"balance_cents"`
	CreatedAt     time.Time `json:"created_at"`
}

func (a apiHandlers) deposit(w http.ResponseWriter, r *http.Request) {
	input, err := depositInputOf(w, r)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	result, err := a.useCases.Deposit.Execute(r.Context(), input)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	response := depositResponse{
		TransactionID: result.TransactionID.String(),
		AccountID:     input.AccountID.String(),
		BalanceCents:  int64(result.Balance.Cents()),
		CreatedAt:     result.CreatedAt.UTC(),
	}
	writeJSON(w, http.StatusCreated, response)
}

type transferRequest struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	AmountCents   int64  `json:"amount_cents"`
}

type transferResponse struct {
	TransactionID    string    `json:"transaction_id"`
	FromBalanceCents int64     `json:"from_balance_cents"`
	CreatedAt        time.Time `json:"created_at"`
}

func (a apiHandlers) transfer(w http.ResponseWriter, r *http.Request) {
	input, err := transferInputOf(w, r)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	result, err := a.useCases.Transfer.Execute(r.Context(), input)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	response := transferResponse{
		TransactionID:    result.TransactionID.String(),
		FromBalanceCents: int64(result.FromBalance.Cents()),
		CreatedAt:        result.CreatedAt.UTC(),
	}
	writeJSON(w, http.StatusCreated, response)
}

func depositInputOf(w http.ResponseWriter, r *http.Request) (application.DepositInput, error) {
	write, err := writeContextOf(r)
	if err != nil {
		return application.DepositInput{}, err
	}

	var request depositRequest

	err = decodeBody(w, r, &request)
	if err != nil {
		return application.DepositInput{}, err
	}

	accountID, err := ledger.ParseAccountID(request.AccountID)
	if err != nil {
		return application.DepositInput{}, err
	}

	amount, err := money.NewAmount(money.Cents(request.AmountCents))
	if err != nil {
		return application.DepositInput{}, err
	}

	return application.DepositInput{AccountID: accountID, Amount: amount, Write: write}, nil
}

func transferInputOf(w http.ResponseWriter, r *http.Request) (application.TransferInput, error) {
	write, err := writeContextOf(r)
	if err != nil {
		return application.TransferInput{}, err
	}

	var request transferRequest

	err = decodeBody(w, r, &request)
	if err != nil {
		return application.TransferInput{}, err
	}

	from, err := ledger.ParseAccountID(request.FromAccountID)
	if err != nil {
		return application.TransferInput{}, err
	}

	to, err := ledger.ParseAccountID(request.ToAccountID)
	if err != nil {
		return application.TransferInput{}, err
	}

	amount, err := money.NewAmount(money.Cents(request.AmountCents))
	if err != nil {
		return application.TransferInput{}, err
	}

	return application.TransferInput{From: from, To: to, Amount: amount, Write: write}, nil
}

func writeContextOf(r *http.Request) (application.WriteContext, error) {
	key, err := idempotency.ParseKey(r.Header.Get(idempotencyHeader))
	if err != nil {
		return application.WriteContext{}, err
	}

	return application.WriteContext{Key: key, CorrelationID: correlationIDFrom(r.Context())}, nil
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

// decodeBody reads one JSON object, refusing unknown fields and oversized bodies.
func decodeBody(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(target)
	if err != nil {
		return fmt.Errorf("%w: body must be a JSON object with the documented fields", errValidation)
	}

	hasTrailingData := decoder.More()
	if hasTrailingData {
		return fmt.Errorf("%w: body must hold a single JSON object", errValidation)
	}

	return nil
}

func newAccountResponse(view application.AccountView) accountResponse {
	return accountResponse{
		AccountID:    view.Account.ID.String(),
		HolderName:   view.Account.HolderName,
		BalanceCents: int64(view.Balance.Cents()),
		CreatedAt:    view.Account.CreatedAt.UTC(),
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
		AccountSeq:        int64(line.Seq),
		TransactionID:     line.TransactionID.String(),
		Kind:              string(line.TransactionKind),
		AmountCents:       int64(line.Amount),
		BalanceAfterCents: int64(line.BalanceAfter.Cents()),
		CreatedAt:         line.CreatedAt.UTC(),
	}
}
