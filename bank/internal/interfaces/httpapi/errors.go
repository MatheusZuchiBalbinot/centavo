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
	"centavo/bank/internal/domain/transfer"
)

// errorCode is the machine-readable code clients branch on (docs/domain.md,
// section 12). Only 4xx codes are definitive; every 5xx is indeterminate.
type errorCode string

const (
	codeValidationFailed       errorCode = "VALIDATION_FAILED"
	codeIdempotencyKeyMissing  errorCode = "IDEMPOTENCY_KEY_MISSING"
	codeAccountNotFound        errorCode = "ACCOUNT_NOT_FOUND"
	codeTransferNotFound       errorCode = "TRANSFER_NOT_FOUND"
	codeSameAccountTransfer    errorCode = "SAME_ACCOUNT_TRANSFER"
	codeInsufficientFunds      errorCode = "INSUFFICIENT_FUNDS"
	codeAmountAboveLimit       errorCode = "AMOUNT_ABOVE_LIMIT"
	codeKeyMismatch            errorCode = "IDEMPOTENCY_KEY_MISMATCH"
	codeOperationVoided        errorCode = "OPERATION_VOIDED"
	codeRequestInProgress      errorCode = "REQUEST_IN_PROGRESS"
	codeOutcomeUnknown         errorCode = "OUTCOME_UNKNOWN"
	codeTemporarilyUnavailable errorCode = "TEMPORARILY_UNAVAILABLE"
	codeInternal               errorCode = "INTERNAL"
)

const (
	unavailableMessage    = "the bank could not finish the request in time; its outcome is unknown"
	outcomeUnknownMessage = "the bank could not confirm whether the write was applied; resolve the operation or retry with the same key"
	internalMessage       = "internal error; the outcome of the request is unknown"
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
	{target: transfer.ErrTransferNotFound, status: http.StatusNotFound, code: codeTransferNotFound},
	{target: ledger.ErrSameAccount, status: http.StatusUnprocessableEntity, code: codeSameAccountTransfer},
	{target: ledger.ErrInsufficientFunds, status: http.StatusUnprocessableEntity, code: codeInsufficientFunds},
	{target: money.ErrAmountAboveLimit, status: http.StatusUnprocessableEntity, code: codeAmountAboveLimit},
	{target: idempotency.ErrKeyMismatch, status: http.StatusUnprocessableEntity, code: codeKeyMismatch},
	{target: idempotency.ErrOperationVoided, status: http.StatusConflict, code: codeOperationVoided},
	{target: idempotency.ErrRequestInProgress, status: http.StatusConflict, code: codeRequestInProgress},
	{target: application.ErrOutcomeUnknown, status: http.StatusServiceUnavailable, code: codeOutcomeUnknown},
	{target: application.ErrTransient, status: http.StatusServiceUnavailable, code: codeTemporarilyUnavailable},
	{target: application.ErrUnavailable, status: http.StatusServiceUnavailable, code: codeTemporarilyUnavailable},
}

// fixedMessages replace the error text of 5xx codes: their causes are
// internal and never reach the client.
var fixedMessages = map[errorCode]string{
	codeOutcomeUnknown: outcomeUnknownMessage, codeTemporarilyUnavailable: unavailableMessage,
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
		detail.Message = clientMessage(mapping, err)

		fixed, isFixed := fixedMessages[mapping.code]
		if isFixed {
			logger.WarnContext(ctx, "request outcome uncertain", "correlation_id", detail.CorrelationID, "code", mapping.code, "error", err)
			detail.Message = fixed
		}

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

// clientMessage is the sentinel's own text: an error wrapped on the way up
// also carries the internal steps it went through, which are no business of
// the client and change with every refactoring. A validation error is the
// exception: its wrapped text says which field is wrong.
func clientMessage(mapping errorMapping, err error) string {
	if errors.Is(mapping.target, errValidation) {
		return err.Error()
	}

	return mapping.target.Error()
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	// The status line is already written; an encode error means the client is gone.
	_ = json.NewEncoder(w).Encode(body)
}
