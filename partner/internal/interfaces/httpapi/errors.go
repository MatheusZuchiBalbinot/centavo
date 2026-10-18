package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"centavo/partner/internal/truth"
)

const (
	codeInvalidRequest     errorCode = "invalid_request"
	codeNotFound           errorCode = "not_found"
	codeUnavailable        errorCode = "unavailable"
	codeInternal           errorCode = "internal"
	internalMessage                  = "internal error"
	unavailableMessage               = "the partner is unavailable"
	statementQueryMessage            = "participant_id, after_seq >= 0 and limit in 1..1000"
	incomingRequestMessage           = "participant_id, end_to_end_id, account_id and amount_cents are required"
)

var (
	errInvalidRequest        = errors.New("invalid request")
	errInvalidStatementQuery = errors.New(statementQueryMessage)
	errInvalidIncoming       = errors.New(incomingRequestMessage)
	errUnknownParticipant    = errors.New("unknown participant_id")
	errInvalidSince          = errors.New("since must be RFC 3339")
	errUnavailable           = errors.New(unavailableMessage)
	// errAnswerAfterProcess is the partner's lie: it processed the transfer and
	// answers 500 anyway.
	errAnswerAfterProcess = errors.New(internalMessage)
)

// errorMappings is the one place that turns an error into a status and a
// code; an error missing here is a 500.
var errorMappings = []errorMapping{
	{target: errInvalidRequest, status: http.StatusBadRequest, code: codeInvalidRequest},
	{target: errInvalidStatementQuery, status: http.StatusBadRequest, code: codeInvalidRequest},
	{target: errInvalidIncoming, status: http.StatusBadRequest, code: codeInvalidRequest},
	{target: errUnknownParticipant, status: http.StatusBadRequest, code: codeInvalidRequest},
	{target: errInvalidSince, status: http.StatusBadRequest, code: codeInvalidRequest},
	{target: truth.ErrNotFound, status: http.StatusNotFound, code: codeNotFound},
	{target: errUnavailable, status: http.StatusServiceUnavailable, code: codeUnavailable},
	{target: errAnswerAfterProcess, status: http.StatusInternalServerError, code: codeInternal},
}

// errorCode is the machine-readable part of an error answer; clients branch
// on it, never on the message.
type errorCode string

type errorMapping struct {
	target error
	status int
	code   errorCode
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    errorCode `json:"code"`
	Message string    `json:"message"`
}

// writeError answers err with its mapped status and code. The message is the
// rule the request broke, never the wrapped detail; an unmapped error is
// logged and answered as a bare 500.
func writeError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	if writeKnownError(w, err) {
		return
	}

	logger.ErrorContext(r.Context(), "unmapped error", "path", r.URL.Path, "error", err)

	detail := errorDetail{Code: codeInternal, Message: internalMessage}
	writeJSON(w, http.StatusInternalServerError, errorBody{Error: detail})
}

// writeKnownError answers err when it has a mapping and reports whether it did.
func writeKnownError(w http.ResponseWriter, err error) bool {
	for _, mapping := range errorMappings {
		if !errors.Is(err, mapping.target) {
			continue
		}

		detail := errorDetail{Code: mapping.code, Message: mapping.target.Error()}
		writeJSON(w, mapping.status, errorBody{Error: detail})

		return true
	}

	return false
}
