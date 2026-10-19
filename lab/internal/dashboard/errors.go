package dashboard

import (
	"errors"
	"net/http"

	"centavo/lab/internal/attacks"
	"centavo/lab/internal/runner"
)

// apiError is a failure the client can act on: the status, and the code it
// branches on. The message only describes the rule.
type apiError struct {
	status  int
	code    string
	message string
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e apiError) Error() string {
	return e.message
}

// The panel API's errors, in one place; clients branch on the code.
var (
	errInvalidRequest   = apiError{http.StatusBadRequest, "invalid_request", "the request body or query is not valid"}
	errUnsupportedMedia = apiError{http.StatusUnsupportedMediaType, "unsupported_media_type", "send the body as application/json"}
	errBodyTooLarge     = apiError{http.StatusRequestEntityTooLarge, "body_too_large", "the request body is too large"}
	errForeignOrigin    = apiError{http.StatusForbidden, "forbidden_origin", "the request came from another site"}
	errRateLimited      = apiError{http.StatusTooManyRequests, "rate_limited", "too many rounds started in the last hour; try again later"}
	errNotFound         = apiError{http.StatusNotFound, "not_found", "there is no such round"}
	errRoundRunning     = apiError{http.StatusConflict, "round_running", "a round is already running; watch it or wait for its verdict"}
	errLabIdle          = apiError{http.StatusConflict, "lab_idle", "no round is running"}
	errUnknownAttack    = apiError{http.StatusUnprocessableEntity, "unknown_attack", "the attack is not on the menu"}
	errInvalidDuration  = apiError{http.StatusUnprocessableEntity, "invalid_duration", "the duration is not one the panel offers"}
	errTooManyStreams   = apiError{http.StatusServiceUnavailable, "too_many_streams", "too many people are watching; try again in a moment"}
	errInternal         = apiError{http.StatusInternalServerError, "internal", "the lab could not answer; its log has the details"}
)

// apiErrorOf translates an error into what the client sees; anything not
// listed is an internal error, whose detail stays in the log.
func apiErrorOf(err error) apiError {
	var known apiError

	switch {
	case errors.As(err, &known):
		return known
	case errors.Is(err, runner.ErrBusy):
		return errRoundRunning
	case errors.Is(err, runner.ErrIdle):
		return errLabIdle
	case errors.Is(err, attacks.ErrUnknown):
		return errUnknownAttack
	case errors.Is(err, attacks.ErrDuration):
		return errInvalidDuration
	default:
		return errInternal
	}
}

func (p panelHandlers) writeError(w http.ResponseWriter, r *http.Request, err error) {
	failure := apiErrorOf(err)

	if failure.status >= http.StatusInternalServerError {
		p.config.Logger.ErrorContext(r.Context(), "panel request failed", "path", r.URL.Path, "error", err)
	}

	writeJSON(w, failure.status, errorBody{Error: errorDetail{Code: failure.code, Message: failure.message}})
}
