package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/interfaces/httpapi"
)

const validKey = "r0001-p01-000001"

var errStorageDown = errors.New("connection refused to 10.0.0.5:5432 by bank_app")

// resolvedDeposit is the request the fake resolver knows as applied.
const resolvedDeposit = `{"account_id":"a","amount_cents":100}`

type fakeTransfer struct {
	received application.TransferInput
	result   application.TransferResult
	err      error
}

type fakeDeposit struct {
	err error
}

// fakeWriter runs the write once, as a naive writer without storage would;
// the key semantics are covered by the integration tests.
type fakeWriter struct {
	lastWrite application.Write
	err       error
}

type fakeResolver struct{}

type fakeStatement struct {
	received application.StatementQuery
}

type unusedAccounts struct{}

type fakeOpenAccount struct{}

type apiFixture struct {
	handler   http.Handler
	transfer  *fakeTransfer
	statement *fakeStatement
	writer    *fakeWriter
}

type apiRequest struct {
	method  string
	path    string
	body    string
	headers map[string]string
}

type errorEnvelope struct {
	Error struct {
		Code          string `json:"code"`
		Message       string `json:"message"`
		CorrelationID string `json:"correlation_id"`
	} `json:"error"`
}

func TestTransfer_PassesTheRequestToTheUseCase(t *testing.T) {
	fixture := newFixture(nil)
	from, to := newAccountIDs(t)
	fixture.transfer.result = application.TransferResult{FromBalance: money.NewBalance(8_750), CreatedAt: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	body := `{"from_account_id": "` + from + `", "to_account_id": "` + to + `", "amount_cents": 1250}`
	headers := map[string]string{"Idempotency-Key": validKey, "X-Correlation-Id": validKey}

	recorder := fixture.do(t, apiRequest{method: http.MethodPost, path: "/v1/transfers", body: body, headers: headers})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}

	received := fixture.transfer.received
	isForwarded := received.From.String() == from && received.To.String() == to && received.Amount.Cents() == 1_250 && string(received.Write.Key) == validKey && received.Write.CorrelationID == validKey
	if !isForwarded {
		t.Fatalf("use case received %+v", received)
	}

	if !strings.Contains(recorder.Body.String(), `"from_balance_cents":8750`) {
		t.Fatalf("response body: %s", recorder.Body)
	}

	if recorder.Header().Get("X-Correlation-Id") != validKey {
		t.Fatalf("correlation id not echoed: %q", recorder.Header().Get("X-Correlation-Id"))
	}
}

func TestWrites_MapErrorsToStableCodes(t *testing.T) {
	from, to := newAccountIDs(t)
	transferBody := func(amount string) string {
		return `{"from_account_id": "` + from + `", "to_account_id": "` + to + `", "amount_cents": ` + amount + `}`
	}
	withKey := map[string]string{"Idempotency-Key": validKey}

	cases := []struct {
		name        string
		request     apiRequest
		transferErr error
		depositErr  error
		wantStatus  int
		wantCode    string
	}{
		{name: "missing key", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: transferBody("10")}, wantStatus: 400, wantCode: "IDEMPOTENCY_KEY_MISSING"},
		{name: "malformed key", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: transferBody("10"), headers: map[string]string{"Idempotency-Key": "bad key"}}, wantStatus: 400, wantCode: "VALIDATION_FAILED"},
		{name: "unknown field", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: `{"from_account_id": "` + from + `", "is_admin": true}`, headers: withKey}, wantStatus: 400, wantCode: "VALIDATION_FAILED"},
		{name: "two objects", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: transferBody("10") + transferBody("10"), headers: withKey}, wantStatus: 400, wantCode: "VALIDATION_FAILED"},
		{name: "zero amount", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: transferBody("0"), headers: withKey}, wantStatus: 400, wantCode: "VALIDATION_FAILED"},
		{name: "amount above limit", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: transferBody("100000001"), headers: withKey}, wantStatus: 422, wantCode: "AMOUNT_ABOVE_LIMIT"},
		{name: "insufficient funds", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: transferBody("10"), headers: withKey}, transferErr: ledger.ErrInsufficientFunds, wantStatus: 422, wantCode: "INSUFFICIENT_FUNDS"},
		{name: "same account", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: transferBody("10"), headers: withKey}, transferErr: ledger.ErrSameAccount, wantStatus: 422, wantCode: "SAME_ACCOUNT_TRANSFER"},
		{name: "unknown account", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: transferBody("10"), headers: withKey}, transferErr: ledger.ErrAccountNotFound, wantStatus: 404, wantCode: "ACCOUNT_NOT_FOUND"},
		{name: "deadline", request: apiRequest{method: http.MethodPost, path: "/v1/transfers", body: transferBody("10"), headers: withKey}, transferErr: context.DeadlineExceeded, wantStatus: 503, wantCode: "TEMPORARILY_UNAVAILABLE"},
		{name: "storage failure", request: apiRequest{method: http.MethodPost, path: "/v1/deposits", body: `{"account_id": "` + from + `", "amount_cents": 10}`, headers: withKey}, depositErr: errStorageDown, wantStatus: 500, wantCode: "INTERNAL"},
		{name: "bad correlation id", request: apiRequest{method: http.MethodPost, path: "/v1/deposits", body: `{}`, headers: map[string]string{"Idempotency-Key": validKey, "X-Correlation-Id": "line\nbreak"}}, wantStatus: 400, wantCode: "VALIDATION_FAILED"},
		{name: "invalid holder name", request: apiRequest{method: http.MethodPost, path: "/v1/accounts", body: `{"holder_name": "   "}`, headers: withKey}, wantStatus: 400, wantCode: "VALIDATION_FAILED"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFixture(tc.depositErr)
			fixture.transfer.err = tc.transferErr

			recorder := fixture.do(t, tc.request)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status: got %d, want %d (%s)", recorder.Code, tc.wantStatus, recorder.Body)
			}

			envelope := decodeError(t, recorder)
			if envelope.Error.Code != tc.wantCode {
				t.Fatalf("code: got %q, want %q", envelope.Error.Code, tc.wantCode)
			}
		})
	}
}

func TestInternalError_NeverLeaksTheCause(t *testing.T) {
	fixture := newFixture(errStorageDown)
	from, _ := newAccountIDs(t)
	headers := map[string]string{"Idempotency-Key": validKey, "X-Correlation-Id": validKey}
	request := apiRequest{method: http.MethodPost, path: "/v1/deposits", body: `{"account_id": "` + from + `", "amount_cents": 10}`, headers: headers}

	recorder := fixture.do(t, request)

	body := recorder.Body.String()
	if strings.Contains(body, "10.0.0.5") || strings.Contains(body, "bank_app") {
		t.Fatalf("500 body leaks the cause: %s", body)
	}

	envelope := decodeError(t, recorder)
	if envelope.Error.CorrelationID != validKey {
		t.Fatalf("correlation id: got %q", envelope.Error.CorrelationID)
	}
}

func TestStatement_ParsesQueryParameters(t *testing.T) {
	from, _ := newAccountIDs(t)

	cases := []struct {
		name       string
		query      string
		wantStatus int
		wantQuery  application.StatementQuery
	}{
		{name: "defaults", query: "", wantStatus: 200, wantQuery: application.StatementQuery{AfterSeq: 0, Limit: application.DefaultStatementLimit}},
		{name: "explicit", query: "?after_seq=7&limit=20", wantStatus: 200, wantQuery: application.StatementQuery{AfterSeq: 7, Limit: 20}},
		{name: "not a number", query: "?limit=ten", wantStatus: 400},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFixture(nil)

			recorder := fixture.do(t, apiRequest{method: http.MethodGet, path: "/v1/accounts/" + from + "/statement" + tc.query})

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status: got %d, want %d (%s)", recorder.Code, tc.wantStatus, recorder.Body)
			}

			isSuccess := tc.wantStatus == http.StatusOK
			if !isSuccess {
				return
			}

			received := fixture.statement.received
			isUnexpected := received.AfterSeq != tc.wantQuery.AfterSeq || received.Limit != tc.wantQuery.Limit
			if isUnexpected {
				t.Fatalf("use case received %+v", received)
			}

			if !strings.Contains(recorder.Body.String(), `"entries":[]`) {
				t.Fatalf("an empty page must be an empty list: %s", recorder.Body)
			}
		})
	}
}

func TestWrites_MapIdempotencyOutcomes(t *testing.T) {
	from, _ := newAccountIDs(t)
	body := `{"account_id": "` + from + `", "amount_cents": 10}`
	headers := map[string]string{"Idempotency-Key": validKey}

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "reused key", err: idempotency.ErrKeyMismatch, wantStatus: 422, wantCode: "IDEMPOTENCY_KEY_MISMATCH"},
		{name: "voided key", err: idempotency.ErrOperationVoided, wantStatus: 409, wantCode: "OPERATION_VOIDED"},
		{name: "key in use", err: idempotency.ErrRequestInProgress, wantStatus: 409, wantCode: "REQUEST_IN_PROGRESS"},
		{name: "ambiguous commit", err: fmt.Errorf("%w: unexpected EOF from 10.0.0.5", application.ErrOutcomeUnknown), wantStatus: 503, wantCode: "OUTCOME_UNKNOWN"},
		{name: "retries exhausted", err: application.ErrTransient, wantStatus: 503, wantCode: "TEMPORARILY_UNAVAILABLE"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFixture(nil)
			fixture.writer.err = tc.err

			recorder := fixture.do(t, apiRequest{method: http.MethodPost, path: "/v1/deposits", body: body, headers: headers})

			envelope := decodeError(t, recorder)
			isUnexpected := recorder.Code != tc.wantStatus || envelope.Error.Code != tc.wantCode
			if isUnexpected {
				t.Fatalf("got %d %s, want %d %s", recorder.Code, envelope.Error.Code, tc.wantStatus, tc.wantCode)
			}

			if strings.Contains(envelope.Error.Message, "10.0.0.5") {
				t.Fatalf("the message leaks the cause: %q", envelope.Error.Message)
			}
		})
	}
}

func TestWrites_BindTheKeyToTheEndpointAndRawBody(t *testing.T) {
	fixture := newFixture(nil)
	from, _ := newAccountIDs(t)
	body := `{"account_id": "` + from + `", "amount_cents": 10}`

	fixture.do(t, apiRequest{method: http.MethodPost, path: "/v1/deposits", body: body, headers: map[string]string{"Idempotency-Key": validKey}})

	write := fixture.writer.lastWrite
	isBound := string(write.Key) == validKey && write.Endpoint == "/v1/deposits" && write.RequestHash == idempotency.HashRequest("/v1/deposits", []byte(body))
	if !isBound {
		t.Fatalf("write: key %q endpoint %q", write.Key, write.Endpoint)
	}
}

func TestResolve_AnswersAppliedOrVoided(t *testing.T) {
	fixture := newFixture(nil)
	resolveBody := `{"endpoint":"/v1/deposits","body":` + resolvedDeposit + `}`

	applied := fixture.do(t, apiRequest{method: http.MethodPost, path: "/v1/operations/" + validKey + "/resolve", body: resolveBody})
	isUnexpected := applied.Code != http.StatusOK || !strings.Contains(applied.Body.String(), `"outcome":"applied"`) || !strings.Contains(applied.Body.String(), `"transaction_id":"t"`)
	if isUnexpected {
		t.Fatalf("applied: %d %s", applied.Code, applied.Body)
	}

	superseded := fixture.do(t, apiRequest{method: http.MethodPost, path: "/v1/operations/" + validKey + "/resolve", body: `{"endpoint":"/v1/deposits","body":{"account_id":"a","amount_cents":999}}`})
	isUnexpected = superseded.Code != http.StatusOK || !strings.Contains(superseded.Body.String(), `"outcome":"superseded"`)
	if isUnexpected {
		t.Fatalf("another request's key: %d %s", superseded.Code, superseded.Body)
	}

	voided := fixture.do(t, apiRequest{method: http.MethodPost, path: "/v1/operations/r0001-p01-000404/resolve", body: resolveBody})
	isUnexpected = voided.Code != http.StatusOK || !strings.Contains(voided.Body.String(), `"outcome":"voided"`)
	if isUnexpected {
		t.Fatalf("voided: %d %s", voided.Code, voided.Body)
	}

	invalid := fixture.do(t, apiRequest{method: http.MethodPost, path: "/v1/operations/short/resolve", body: resolveBody})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid key: %d", invalid.Code)
	}

	withoutRequest := fixture.do(t, apiRequest{method: http.MethodPost, path: "/v1/operations/" + validKey + "/resolve", body: `{}`})
	if withoutRequest.Code != http.StatusBadRequest {
		t.Fatalf("resolve without the request: %d", withoutRequest.Code)
	}
}

// TestClientError_SaysTheRuleNotThePath: a domain error wrapped by the
// storage layer reaches the client as the rule it broke, without the
// internal step that carried it.
func TestClientError_SaysTheRuleNotThePath(t *testing.T) {
	fixture := newFixture(nil)
	fixture.transfer.err = fmt.Errorf("reading customer position: %w", ledger.ErrAccountNotFound)
	from, to := newAccountIDs(t)
	body := `{"from_account_id": "` + from + `", "to_account_id": "` + to + `", "amount_cents": 10}`
	request := apiRequest{method: http.MethodPost, path: "/v1/transfers", body: body, headers: map[string]string{"Idempotency-Key": validKey}}

	recorder := fixture.do(t, request)

	envelope := decodeError(t, recorder)
	if envelope.Error.Message != ledger.ErrAccountNotFound.Error() {
		t.Fatalf("message %q, want %q", envelope.Error.Message, ledger.ErrAccountNotFound.Error())
	}
}

func (f *fakeTransfer) Apply(_ context.Context, _ application.MoneyStores, _ ledger.OperationID, input application.TransferInput) (application.TransferResult, error) {
	f.received = input

	return f.result, f.err
}

func (f fakeDeposit) Apply(_ context.Context, _ application.MoneyStores, _ ledger.OperationID, input application.DepositInput) (application.DepositResult, error) {
	return application.DepositResult{Balance: money.NewBalance(input.Amount.Cents())}, f.err
}

func (f *fakeWriter) Execute(ctx context.Context, write application.Write) (idempotency.StoredResponse, error) {
	f.lastWrite = write

	if f.err != nil {
		return idempotency.StoredResponse{}, f.err
	}

	return write.Apply(ctx, nil, ledger.OperationID{})
}

func (fakeResolver) Resolve(_ context.Context, request application.ResolveRequest) (application.Resolution, error) {
	if request.Key == "r0001-p01-000404" {
		return application.Resolution{Outcome: idempotency.OutcomeVoided}, nil
	}

	isTheAppliedRequest := request.Endpoint == "/v1/deposits" && request.RequestHash == idempotency.HashRequest("/v1/deposits", []byte(resolvedDeposit))
	if !isTheAppliedRequest {
		return application.Resolution{Outcome: idempotency.OutcomeSuperseded}, nil
	}

	stored := idempotency.StoredResponse{Status: 201, Body: []byte(`{"transaction_id":"t"}`)}

	return application.Resolution{Outcome: idempotency.OutcomeApplied, Response: &stored}, nil
}

func (f *fakeStatement) Execute(_ context.Context, query application.StatementQuery) (application.StatementPage, error) {
	f.received = query

	return application.StatementPage{}, nil
}

func (unusedAccounts) Execute(context.Context, ledger.AccountID) (application.AccountView, error) {
	return application.AccountView{}, ledger.ErrAccountNotFound
}

func (fakeOpenAccount) Apply(_ context.Context, _ application.CustomerCreator, holderName string) (application.AccountView, error) {
	name, err := ledger.ParseHolderName(holderName)

	return application.AccountView{Account: ledger.Account{HolderName: name}}, err
}

func newFixture(depositErr error) apiFixture {
	transfer := &fakeTransfer{}
	statement := &fakeStatement{}
	writer := &fakeWriter{}
	useCases := httpapi.UseCases{
		Writer: writer, Resolver: fakeResolver{}, OpenAccount: fakeOpenAccount{}, GetAccount: unusedAccounts{},
		GetStatement: statement, Deposit: fakeDeposit{err: depositErr}, Transfer: transfer,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	return apiFixture{handler: httpapi.NewAPIHandler(useCases, logger), transfer: transfer, statement: statement, writer: writer}
}

func (f apiFixture) do(t *testing.T, request apiRequest) *httptest.ResponseRecorder {
	t.Helper()

	httpRequest := httptest.NewRequestWithContext(t.Context(), request.method, request.path, strings.NewReader(request.body))
	for name, value := range request.headers {
		httpRequest.Header.Set(name, value)
	}

	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, httpRequest)

	return recorder
}

func decodeError(t *testing.T, recorder *httptest.ResponseRecorder) errorEnvelope {
	t.Helper()

	var envelope errorEnvelope

	err := json.NewDecoder(recorder.Body).Decode(&envelope)
	if err != nil {
		t.Fatalf("error body is not JSON: %v", err)
	}

	return envelope
}

func newAccountIDs(t *testing.T) (string, string) {
	t.Helper()

	first, err := ledger.NewAccountID()
	if err != nil {
		t.Fatal(err)
	}

	second, err := ledger.NewAccountID()
	if err != nil {
		t.Fatal(err)
	}

	return first.String(), second.String()
}
