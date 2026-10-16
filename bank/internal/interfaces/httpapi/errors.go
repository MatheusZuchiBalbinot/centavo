package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

// errorCode is the machine-readable code clients branch on (docs/domain.md,
// section 12). Only 4xx codes are definitive; every 5xx is indeterminate.
type errorCode string

const (
	codeValidationFailed       errorCode = "VALIDATION_FAILED"
	codeIdempotencyKeyMissing  errorCode = "IDEMPOTENCY_KEY_MISSING"
	codeAccountNotFound        errorCode = "ACCOUNT_NOT_FOUND"
	codeSameAccountTransfer    errorCode = "SAME_ACCOUNT_TRANSFER"
	codeInsufficientFunds      errorCode = "INSUFFICIENT_FUNDS"
	codeAmountAboveLimit       errorCode = "AMOUNT_ABOVE_LIMIT"
	codeTemporarilyUnavailable errorCode = "TEMPORARILY_UNAVAILABLE"
	codeInternal               errorCode = "INTERNAL"
)

const (
	unavailableMessage = "the bank could not finish the request in time; its outcome is unknown"
	internalMessage    = "internal error; the outcome of the request is unknown"
)

// errValidation marks malformed input; the wrapped text tells the client what.
var errValidation = errors.New("invalid request")

type errorMapping struct {
	target error
	status int
	code   errorCode
}

// errorMappings is the only place where an error becomes an HTTP status.
var errorMappings = []errorMapping{
	{target: idempotency.ErrKeyMissing, status: http.StatusBadRequest, code: codeIdempotencyKeyMissing},
	{target: idempotency.ErrKeyInvalid, status: http.StatusBadRequest, code: codeValidationFailed},
	{target: errValidation, status: http.StatusBadRequest, code: codeValidationFailed},
	{target: ledger.ErrInvalidAccountID, status: http.StatusBadRequest, code: codeValidationFailed},
	{target: ledger.ErrInvalidHolderName, status: http.StatusBadRequest, code: codeValidationFailed},
	{target: money.ErrAmountNotPositive, status: http.StatusBadRequest, code: codeValidationFailed},
	{target: application.ErrInvalidStatementQuery, status: http.StatusBadRequest, code: codeValidationFailed},
	{target: ledger.ErrAccountNotFound, status: http.StatusNotFound, code: codeAccountNotFound},
	{target: ledger.ErrSameAccount, status: http.StatusUnprocessableEntity, code: codeSameAccountTransfer},
	{target: ledger.ErrInsufficientFunds, status: http.StatusUnprocessableEntity, code: codeInsufficientFunds},
	{target: money.ErrAmountAboveLimit, status: http.StatusUnprocessableEntity, code: codeAmountAboveLimit},
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code          errorCode `json:"code"`
	Message       string    `json:"message"`
	CorrelationID string    `json:"correlation_id"`
}

// writeError answers with the mapped status, or a generic 500 that never
// leaks the cause; the cause goes to the log instead.
func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	ctx := r.Context()
	detail := errorDetail{CorrelationID: correlationIDFrom(ctx)}

	for _, mapping := range errorMappings {
		if !errors.Is(err, mapping.target) {
			continue
		}

		detail.Code = mapping.code
		detail.Message = err.Error()
		writeJSON(w, mapping.status, errorBody{Error: detail})

		return
	}

	isTimeout := errors.Is(err, context.DeadlineExceeded)
	if isTimeout {
		logger.WarnContext(ctx, "request deadline exceeded", "correlation_id", detail.CorrelationID, "error", err)
		detail.Code = codeTemporarilyUnavailable
		detail.Message = unavailableMessage
		writeJSON(w, http.StatusServiceUnavailable, errorBody{Error: detail})

		return
	}

	logger.ErrorContext(ctx, "request failed", "correlation_id", detail.CorrelationID, "method", r.Method, "path", r.URL.Path, "error", err)
	detail.Code = codeInternal
	detail.Message = internalMessage
	writeJSON(w, http.StatusInternalServerError, errorBody{Error: detail})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// The status line is already written; an encode error means the client is gone.
	_ = json.NewEncoder(w).Encode(body)
}
