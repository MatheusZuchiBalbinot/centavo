// Package loadgen drives the bank like its clients would and records every
// request and answer in the history.
package loadgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"
)

// outcomeKind classifies what one HTTP attempt showed the client.
type outcomeKind string

const (
	outcomeDomain            outcomeKind = "domain"
	outcomeRequestInProgress outcomeKind = "request_in_progress"
	outcomeTimeout           outcomeKind = "timeout"
	outcomeConnectionError   outcomeKind = "connection_error"
	outcomeServerError       outcomeKind = "server_error"
	outcomeInvalidBody       outcomeKind = "invalid_body"
	outcomeUnexpected        outcomeKind = "unexpected_response"
)

// verdict is what the client does after an attempt.
type verdict int

const (
	verdictSucceeded verdict = iota
	// verdictFailed is a definitive 4xx: the bank guarantees nothing was applied.
	verdictFailed
	// verdictRetry is any doubt about the outcome; retrying with the same key
	// is what a polite client does.
	verdictRetry
	// verdictGiveUp is an answer the client does not understand; it never
	// becomes a certainty, and retrying would not make it one.
	verdictGiveUp
)

// maxResponseBytes bounds what the client reads from one response.
const maxResponseBytes = 1 << 20

// definitiveCodes are the 4xx codes after which nothing was applied. The lab
// keeps its own list: it never imports the bank.
var definitiveCodes = map[string]bool{
	"VALIDATION_FAILED": true, "IDEMPOTENCY_KEY_MISSING": true, "ACCOUNT_NOT_FOUND": true, "TRANSFER_NOT_FOUND": true,
	"SAME_ACCOUNT_TRANSFER": true, "INSUFFICIENT_FUNDS": true, "AMOUNT_ABOVE_LIMIT": true, "IDEMPOTENCY_KEY_MISMATCH": true, "OPERATION_VOIDED": true,
}

const requestInProgressCode = "REQUEST_IN_PROGRESS"

// outcome is how history.jsonl records an attempt or a failed operation.
type outcome struct {
	Kind       outcomeKind `json:"kind"`
	HTTPStatus int         `json:"http_status,omitempty"`
	Code       string      `json:"code,omitempty"`
	AfterMS    int64       `json:"after_ms,omitempty"`
}

type attemptResult struct {
	verdict verdict
	outcome outcome
	value   json.RawMessage
}

// request is one HTTP call to the bank.
type request struct {
	method        string
	path          string
	body          []byte
	key           string
	correlationID string
}

// bankClient sends requests to the bank's client API.
type bankClient struct {
	baseURL string
	http    *http.Client
}

func newBankClient(baseURL string, connections int) bankClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = connections

	return bankClient{baseURL: baseURL, http: &http.Client{Transport: transport}}
}

type errorEnvelope struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

// attempt sends the request once and classifies what came back. parse turns a
// 2xx body into the value the history records, or fails when fields are missing.
func (c bankClient) attempt(ctx context.Context, req request, timeout time.Duration, parse parseFunc) attemptResult {
	started := time.Now()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpRequest, err := http.NewRequestWithContext(ctx, req.method, c.baseURL+req.path, bytes.NewReader(req.body))
	if err != nil {
		return attemptResult{verdict: verdictGiveUp, outcome: outcome{Kind: outcomeUnexpected}}
	}

	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("X-Correlation-Id", req.correlationID)

	if req.key != "" {
		httpRequest.Header.Set("Idempotency-Key", req.key)
	}

	response, err := c.http.Do(httpRequest)
	if err != nil {
		return transportFailure(err, started)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return attemptResult{verdict: verdictRetry, outcome: outcome{Kind: outcomeInvalidBody, HTTPStatus: response.StatusCode}}
	}

	return classify(response.StatusCode, body, parse)
}

func transportFailure(err error, started time.Time) attemptResult {
	isTimeout := errors.Is(err, context.DeadlineExceeded)
	if isTimeout {
		return attemptResult{verdict: verdictRetry, outcome: outcome{Kind: outcomeTimeout, AfterMS: time.Since(started).Milliseconds()}}
	}

	return attemptResult{verdict: verdictRetry, outcome: outcome{Kind: outcomeConnectionError}}
}

func classify(status int, body []byte, parse parseFunc) attemptResult {
	isSuccess := status >= http.StatusOK && status < http.StatusMultipleChoices
	if isSuccess {
		return classifySuccess(status, body, parse)
	}

	isServerError := status >= http.StatusInternalServerError
	if isServerError {
		return attemptResult{verdict: verdictRetry, outcome: outcome{Kind: outcomeServerError, HTTPStatus: status, Code: errorCodeOf(body)}}
	}

	code := errorCodeOf(body)

	if code == requestInProgressCode {
		return attemptResult{verdict: verdictRetry, outcome: outcome{Kind: outcomeRequestInProgress, HTTPStatus: status, Code: code}}
	}

	isDefinitive := status >= http.StatusBadRequest && definitiveCodes[code]
	if isDefinitive {
		return attemptResult{verdict: verdictFailed, outcome: outcome{Kind: outcomeDomain, HTTPStatus: status, Code: code}}
	}

	return attemptResult{verdict: verdictGiveUp, outcome: outcome{Kind: outcomeUnexpected, HTTPStatus: status, Code: code}}
}

func classifySuccess(status int, body []byte, parse parseFunc) attemptResult {
	value, err := parse(body, status)
	if err != nil {
		return attemptResult{verdict: verdictRetry, outcome: outcome{Kind: outcomeInvalidBody, HTTPStatus: status}}
	}

	return attemptResult{verdict: verdictSucceeded, value: value}
}

func errorCodeOf(body []byte) string {
	var envelope errorEnvelope

	err := json.Unmarshal(body, &envelope)
	if err != nil {
		return ""
	}

	return envelope.Error.Code
}
