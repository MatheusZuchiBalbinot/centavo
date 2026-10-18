package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/platform/failpoint"
)

const (
	idempotencyHeader = "Idempotency-Key"
	// maxBodyBytes is far above any valid request body of the API.
	maxBodyBytes = 16 << 10
)

type openAccountRequest struct {
	HolderName string `json:"holder_name"`
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

// applyFunc runs a use case inside the writer's unit of work and encodes the
// answer that the key will keep.
type applyFunc func(ctx context.Context, stores application.Stores, operationID ledger.OperationID) (idempotency.StoredResponse, error)

func (a apiHandlers) openAccount(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	var request openAccountRequest

	err = decodeBody(body, &request)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	a.runWrite(w, r, writeRequest{endpoint: accountsEndpoint, body: body, apply: a.applyOpenAccount(request.HolderName)})
}

func (a apiHandlers) applyOpenAccount(holderName string) applyFunc {
	return func(ctx context.Context, stores application.Stores, _ ledger.OperationID) (idempotency.StoredResponse, error) {
		view, err := a.useCases.OpenAccount.Apply(ctx, stores, holderName)
		if err != nil {
			return idempotency.StoredResponse{}, err
		}

		return storedJSON(http.StatusCreated, newAccountResponse(view))
	}
}

func (a apiHandlers) deposit(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	input, err := depositInputOf(r, body)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	a.runWrite(w, r, writeRequest{endpoint: depositsEndpoint, body: body, apply: a.applyDeposit(input)})
}

func (a apiHandlers) applyDeposit(input application.DepositInput) applyFunc {
	return func(ctx context.Context, stores application.Stores, operationID ledger.OperationID) (idempotency.StoredResponse, error) {
		result, err := a.useCases.Deposit.Apply(ctx, stores, operationID, input)
		if err != nil {
			return idempotency.StoredResponse{}, err
		}

		response := depositResponse{
			TransactionID: result.TransactionID.String(), AccountID: input.AccountID.String(),
			BalanceCents: int64(result.Balance.Cents()), CreatedAt: result.CreatedAt.UTC(),
		}

		return storedJSON(http.StatusCreated, response)
	}
}

func (a apiHandlers) transfer(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	input, err := transferInputOf(r, body)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	a.runWrite(w, r, writeRequest{endpoint: transfersEndpoint, body: body, apply: a.applyTransfer(input)})
}

func (a apiHandlers) applyTransfer(input application.TransferInput) applyFunc {
	return func(ctx context.Context, stores application.Stores, operationID ledger.OperationID) (idempotency.StoredResponse, error) {
		result, err := a.useCases.Transfer.Apply(ctx, stores, operationID, input)
		if err != nil {
			return idempotency.StoredResponse{}, err
		}

		response := transferResponse{
			TransactionID: result.TransactionID.String(), FromBalanceCents: int64(result.FromBalance.Cents()), CreatedAt: result.CreatedAt.UTC(),
		}

		return storedJSON(http.StatusCreated, response)
	}
}

// writeRequest is one client write as the handler prepared it.
type writeRequest struct {
	endpoint string
	body     []byte
	apply    applyFunc
	// afterCommit runs once the write is committed; nil for most writes.
	afterCommit func(ctx context.Context) error
}

// runWrite runs the write through the writer and sends the stored answer:
// the same bytes on the first try and on every retry.
func (a apiHandlers) runWrite(w http.ResponseWriter, r *http.Request, request writeRequest) {
	key, err := idempotency.ParseKey(r.Header.Get(idempotencyHeader))
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	write := application.Write{Key: key, Endpoint: request.endpoint, RequestHash: idempotency.HashRequest(request.endpoint, request.body), Apply: request.apply}

	response, err := a.useCases.Writer.Execute(r.Context(), write)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	if request.afterCommit != nil {
		err = request.afterCommit(r.Context())
		if err != nil {
			writeError(w, r, a.logger, err)

			return
		}
	}

	// `api.after_commit_before_response`: the write is committed and the answer
	// not sent.
	err = failpoint.Hit(r.Context(), failpoint.APIAfterCommitBeforeResponse)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	writeStored(w, response)
}

// resolveRequest names the request whose outcome the client asks about, as it
// sent it: the key alone cannot tell it from another request that took the same
// key.
type resolveRequest struct {
	Endpoint string          `json:"endpoint"`
	Body     json.RawMessage `json:"body"`
}

// resolvableEndpoints are the writes whose keys resolve can settle.
var resolvableEndpoints = map[string]bool{accountsEndpoint: true, depositsEndpoint: true, transfersEndpoint: true, pixOutEndpoint: true}

type resolveResponse struct {
	Key      string              `json:"key"`
	Outcome  idempotency.Outcome `json:"outcome"`
	Response *storedAnswer       `json:"response,omitempty"`
}

type storedAnswer struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

// resolve answers "applied" or "voided" for an operation whose outcome the
// client does not know.
func (a apiHandlers) resolve(w http.ResponseWriter, r *http.Request) {
	key, err := idempotency.ParseKey(r.PathValue("key"))
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	request, err := resolveRequestOf(w, r, key)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	resolution, err := a.useCases.Resolver.Resolve(r.Context(), request)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	response := resolveResponse{Key: string(key), Outcome: resolution.Outcome}

	hasStoredAnswer := resolution.Response != nil && json.Valid(resolution.Response.Body)
	if hasStoredAnswer {
		response.Response = &storedAnswer{Status: resolution.Response.Status, Body: resolution.Response.Body}
	}

	writeJSON(w, http.StatusOK, response)
}

func resolveRequestOf(w http.ResponseWriter, r *http.Request, key idempotency.Key) (application.ResolveRequest, error) {
	body, err := readBody(w, r)
	if err != nil {
		return application.ResolveRequest{}, err
	}

	var request resolveRequest

	err = decodeBody(body, &request)
	if err != nil {
		return application.ResolveRequest{}, err
	}

	isValid := resolvableEndpoints[request.Endpoint] && len(request.Body) > 0
	if !isValid {
		return application.ResolveRequest{}, fmt.Errorf("%w: endpoint must be a write of the API and body the request it sent", errValidation)
	}

	return application.ResolveRequest{Key: key, Endpoint: request.Endpoint, RequestHash: idempotency.HashRequest(request.Endpoint, request.Body)}, nil
}

func depositInputOf(r *http.Request, body []byte) (application.DepositInput, error) {
	var request depositRequest

	err := decodeBody(body, &request)
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

	return application.DepositInput{AccountID: accountID, Amount: amount, Write: writeContextOf(r)}, nil
}

func transferInputOf(r *http.Request, body []byte) (application.TransferInput, error) {
	var request transferRequest

	err := decodeBody(body, &request)
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

	return application.TransferInput{From: from, To: to, Amount: amount, Write: writeContextOf(r)}, nil
}

// writeContextOf carries the key as client_ref; runWrite has already
// validated it by the time a use case runs.
func writeContextOf(r *http.Request) application.WriteContext {
	key := idempotency.Key(r.Header.Get(idempotencyHeader))

	return application.WriteContext{Key: key, CorrelationID: correlationIDFrom(r.Context())}
}

// readBody reads the raw body, refusing oversized ones; the raw bytes feed
// the request hash, so a retry with the same body matches.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: body is too large or unreadable", errValidation)
	}

	return body, nil
}

// decodeBody reads one JSON object, refusing unknown fields and trailing data.
func decodeBody(body []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(target)
	if err != nil {
		return fmt.Errorf("%w: body must be a JSON object with the documented fields", errValidation)
	}

	if decoder.More() {
		return fmt.Errorf("%w: body must hold a single JSON object", errValidation)
	}

	return nil
}

func storedJSON(status int, body any) (idempotency.StoredResponse, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return idempotency.StoredResponse{}, err
	}

	return idempotency.StoredResponse{Status: status, Body: append(encoded, '\n')}, nil
}

func writeStored(w http.ResponseWriter, response idempotency.StoredResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.Status)

	// The status line is already written; a failed write means the client is gone.
	_, _ = w.Write(response.Body)
}
