package loadgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"centavo/lab/internal/history"
)

// parseFunc turns a 2xx body into the value history.jsonl records; it fails
// when an expected field is missing, which the client treats as a cut response.
type parseFunc func(body []byte, status int) (json.RawMessage, error)

var errMissingField = errors.New("response is missing an expected field")

// operation is one client operation, ready to send and to record.
type operation struct {
	function    history.Function
	request     request
	invokeValue json.RawMessage
	parse       parseFunc
	// reusedKey is the key of an earlier operation that a buggy client sends
	// with this operation's body; the bank must refuse it.
	reusedKey string
	// hasNoKey marks control calls such as resolve, which carry no key.
	hasNoKey bool
	// isToPartner sends the request to the partner instead of the bank.
	isToPartner bool
}

// accountID is the bank's account id, as the lab sees it: opaque text.
type accountID string

type createAccountArgs struct {
	HolderName string `json:"holder_name"`
}

type depositArgs struct {
	AccountID   accountID `json:"account_id"`
	AmountCents int64     `json:"amount_cents"`
}

type transferArgs struct {
	From        accountID `json:"from"`
	To          accountID `json:"to"`
	AmountCents int64     `json:"amount_cents"`
}

type transferBody struct {
	FromAccountID accountID `json:"from_account_id"`
	ToAccountID   accountID `json:"to_account_id"`
	AmountCents   int64     `json:"amount_cents"`
}

type readBalanceArgs struct {
	AccountID accountID `json:"account_id"`
}

type pixOutArgs struct {
	AccountID      accountID `json:"account_id"`
	AmountCents    int64     `json:"amount_cents"`
	DestinationKey string    `json:"destination_key"`
}

type pixInArgs struct {
	AccountID   accountID `json:"account_id"`
	AmountCents int64     `json:"amount_cents"`
	EndToEndID  string    `json:"end_to_end_id"`
}

type pixInBody struct {
	ParticipantID string    `json:"participant_id"`
	EndToEndID    string    `json:"end_to_end_id"`
	AccountID     accountID `json:"account_id"`
	AmountCents   int64     `json:"amount_cents"`
}

type readPixOutArgs struct {
	TransferID string `json:"transfer_id"`
}

type readStatementArgs struct {
	AccountID accountID `json:"account_id"`
	AfterSeq  int64     `json:"after_seq"`
	Limit     int       `json:"limit"`
}

func createAccountOperation(args createAccountArgs) (operation, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return operation{}, err
	}

	writeRequest := request{method: http.MethodPost, path: "/v1/accounts", body: body}

	return newOperation(history.FunctionCreateAccount, writeRequest, args, parseCreateAccount)
}

func depositOperation(args depositArgs) (operation, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return operation{}, err
	}

	writeRequest := request{method: http.MethodPost, path: "/v1/deposits", body: body}

	return newOperation(history.FunctionDeposit, writeRequest, args, parseDeposit)
}

func transferOperation(args transferArgs) (operation, error) {
	body, err := json.Marshal(transferBody{FromAccountID: args.From, ToAccountID: args.To, AmountCents: args.AmountCents})
	if err != nil {
		return operation{}, err
	}

	writeRequest := request{method: http.MethodPost, path: "/v1/transfers", body: body}

	return newOperation(history.FunctionTransfer, writeRequest, args, parseTransfer)
}

func pixOutOperation(args pixOutArgs) (operation, error) {
	body, err := json.Marshal(args)
	if err != nil {
		return operation{}, err
	}

	writeRequest := request{method: http.MethodPost, path: "/v1/external-transfers", body: body}

	return newOperation(history.FunctionPixOut, writeRequest, args, parsePixOut)
}

// pixInOperation asks the partner to send a PIX in; it goes to the partner,
// not to the bank, and carries no idempotency key: the end_to_end_id is it.
func pixInOperation(args pixInArgs, participantID string) (operation, error) {
	body, err := json.Marshal(pixInBody{ParticipantID: participantID, EndToEndID: args.EndToEndID, AccountID: args.AccountID, AmountCents: args.AmountCents})
	if err != nil {
		return operation{}, err
	}

	partnerRequest := request{method: http.MethodPost, path: "/internal/incoming", body: body}

	op, err := newOperation(history.FunctionPixIn, partnerRequest, args, parsePixIn)
	op.hasNoKey = true
	op.isToPartner = true

	return op, err
}

func readPixOutOperation(transferID string) (operation, error) {
	readRequest := request{method: http.MethodGet, path: "/v1/external-transfers/" + url.PathEscape(transferID)}

	return newOperation(history.FunctionReadPixOut, readRequest, readPixOutArgs{TransferID: transferID}, parseReadPixOut)
}

func readBalanceOperation(args readBalanceArgs) (operation, error) {
	readRequest := request{method: http.MethodGet, path: "/v1/accounts/" + url.PathEscape(string(args.AccountID))}

	return newOperation(history.FunctionReadBalance, readRequest, args, parseReadBalance)
}

func readStatementOperation(args readStatementArgs) (operation, error) {
	query := url.Values{}
	query.Set("after_seq", strconv.FormatInt(args.AfterSeq, 10))
	query.Set("limit", strconv.Itoa(args.Limit))

	path := "/v1/accounts/" + url.PathEscape(string(args.AccountID)) + "/statement?" + query.Encode()
	readRequest := request{method: http.MethodGet, path: path}

	return newOperation(history.FunctionReadStatement, readRequest, args, parseReadStatement)
}

func newOperation(function history.Function, req request, args any, parse parseFunc) (operation, error) {
	invokeValue, err := json.Marshal(args)
	if err != nil {
		return operation{}, fmt.Errorf("encoding %s arguments: %w", function, err)
	}

	return operation{function: function, request: req, invokeValue: invokeValue, parse: parse}, nil
}

// isWrite tells which operations carry an Idempotency-Key.
func (o operation) isWrite() bool {
	return o.request.method == http.MethodPost && !o.hasNoKey
}

// reusingKey returns the operation sent under another operation's key; the
// history records which key it reused.
func (o operation) reusingKey(key string) (operation, error) {
	var value map[string]json.RawMessage

	err := json.Unmarshal(o.invokeValue, &value)
	if err != nil {
		return operation{}, err
	}

	value["reused_key"], err = json.Marshal(key)
	if err != nil {
		return operation{}, err
	}

	o.invokeValue, err = json.Marshal(value)
	if err != nil {
		return operation{}, err
	}

	o.reusedKey = key

	return o, nil
}

type createAccountResult struct {
	AccountID  accountID `json:"account_id"`
	HTTPStatus int       `json:"http_status"`
}

func parseCreateAccount(body []byte, status int) (json.RawMessage, error) {
	var response struct {
		AccountID accountID `json:"account_id"`
	}

	err := json.Unmarshal(body, &response)
	if err != nil || response.AccountID == "" {
		return nil, errMissingField
	}

	return json.Marshal(createAccountResult{AccountID: response.AccountID, HTTPStatus: status})
}

type depositResult struct {
	TransactionID string `json:"transaction_id"`
	BalanceCents  int64  `json:"balance_cents"`
	HTTPStatus    int    `json:"http_status"`
}

func parseDeposit(body []byte, status int) (json.RawMessage, error) {
	var response struct {
		TransactionID string `json:"transaction_id"`
		BalanceCents  *int64 `json:"balance_cents"`
	}

	err := json.Unmarshal(body, &response)
	if err != nil || response.TransactionID == "" || response.BalanceCents == nil {
		return nil, errMissingField
	}

	return json.Marshal(depositResult{TransactionID: response.TransactionID, BalanceCents: *response.BalanceCents, HTTPStatus: status})
}

type transferResult struct {
	TransactionID    string `json:"transaction_id"`
	FromBalanceCents int64  `json:"from_balance_cents"`
	HTTPStatus       int    `json:"http_status"`
}

func parseTransfer(body []byte, status int) (json.RawMessage, error) {
	var response struct {
		TransactionID    string `json:"transaction_id"`
		FromBalanceCents *int64 `json:"from_balance_cents"`
	}

	err := json.Unmarshal(body, &response)
	if err != nil || response.TransactionID == "" || response.FromBalanceCents == nil {
		return nil, errMissingField
	}

	return json.Marshal(transferResult{TransactionID: response.TransactionID, FromBalanceCents: *response.FromBalanceCents, HTTPStatus: status})
}

type readBalanceResult struct {
	BalanceCents int64 `json:"balance_cents"`
	HTTPStatus   int   `json:"http_status"`
}

func parseReadBalance(body []byte, status int) (json.RawMessage, error) {
	var response struct {
		BalanceCents *int64 `json:"balance_cents"`
	}

	err := json.Unmarshal(body, &response)
	if err != nil || response.BalanceCents == nil {
		return nil, errMissingField
	}

	return json.Marshal(readBalanceResult{BalanceCents: *response.BalanceCents, HTTPStatus: status})
}

type pixOutResult struct {
	TransferID string `json:"transfer_id"`
	EndToEndID string `json:"end_to_end_id"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status"`
}

func parsePixOut(body []byte, status int) (json.RawMessage, error) {
	var response pixOutResult

	err := json.Unmarshal(body, &response)
	if err != nil || response.TransferID == "" || response.Status == "" {
		return nil, errMissingField
	}

	response.HTTPStatus = status

	return json.Marshal(response)
}

type pixInResult struct {
	IsAccepted bool `json:"accepted"`
	IsNew      bool `json:"new"`
	HTTPStatus int  `json:"http_status"`
}

func parsePixIn(body []byte, status int) (json.RawMessage, error) {
	var response pixInResult

	err := json.Unmarshal(body, &response)
	if err != nil || !response.IsAccepted {
		return nil, errMissingField
	}

	response.HTTPStatus = status

	return json.Marshal(response)
}

// parseReadPixOut keeps the transfer as the bank sent it, transitions included.
func parseReadPixOut(body []byte, status int) (json.RawMessage, error) {
	var response map[string]json.RawMessage

	err := json.Unmarshal(body, &response)
	if err != nil || len(response["status"]) == 0 || len(response["transitions"]) == 0 {
		return nil, errMissingField
	}

	response["http_status"], err = json.Marshal(status)
	if err != nil {
		return nil, err
	}

	return json.Marshal(response)
}

// readStatementResult keeps the page as the bank sent it: the checker
// verifies contiguous positions and chained balances on each page (I4).
type readStatementResult struct {
	Entries      json.RawMessage `json:"entries"`
	NextAfterSeq *int64          `json:"next_after_seq"`
	HTTPStatus   int             `json:"http_status"`
}

func parseReadStatement(body []byte, status int) (json.RawMessage, error) {
	var response struct {
		Entries      json.RawMessage `json:"entries"`
		NextAfterSeq *int64          `json:"next_after_seq"`
	}

	err := json.Unmarshal(body, &response)
	if err != nil || len(response.Entries) == 0 || response.Entries[0] != '[' {
		return nil, errMissingField
	}

	return json.Marshal(readStatementResult{Entries: response.Entries, NextAfterSeq: response.NextAfterSeq, HTTPStatus: status})
}
