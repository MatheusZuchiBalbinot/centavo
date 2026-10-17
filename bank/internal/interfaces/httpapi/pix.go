package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/idempotency"
	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
	"centavo/bank/internal/platform/clock"
	"centavo/bank/internal/platform/failpoint"
)

const (
	pixOutEndpoint  = "/v1/external-transfers"
	signatureHeader = "X-Partner-Signature"
	// signatureTolerance bounds how old a signed webhook may be (docs/architecture.md, section 4.2).
	signatureTolerance = 5 * time.Minute
)

var errBadSignature = errors.New("webhook signature is missing, stale or wrong")

// PixOutCreator creates PIX out inside a unit of work.
type PixOutCreator interface {
	Apply(ctx context.Context, stores application.Stores, operationID ledger.OperationID, input application.PixOutInput) (application.PixOutResult, error)
}

// PixOutGetter reads one PIX out.
type PixOutGetter interface {
	Execute(ctx context.Context, id transfer.ID) (application.ExternalTransferView, error)
}

// WebhookApplier applies a partner event.
type WebhookApplier interface {
	Apply(ctx context.Context, event application.PartnerEvent) error
}

// PixUseCases are the PIX entry points of the API.
type PixUseCases struct {
	Create    PixOutCreator
	Publisher application.Publication
	Get       PixOutGetter
	Webhook   WebhookApplier
	// WebhookSecret signs the partner's webhooks.
	WebhookSecret []byte
}

type pixOutRequest struct {
	AccountID      string `json:"account_id"`
	AmountCents    int64  `json:"amount_cents"`
	DestinationKey string `json:"destination_key"`
}

type pixOutResponse struct {
	TransferID string          `json:"transfer_id"`
	EndToEndID string          `json:"end_to_end_id"`
	Status     transfer.Status `json:"status"`
	CreatedAt  time.Time       `json:"created_at"`
}

func (a apiHandlers) pixOut(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	input, err := pixOutInputOf(r, body)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	publication := &pendingPublication{}
	a.runWrite(w, r, writeRequest{endpoint: pixOutEndpoint, body: body, apply: a.applyPixOut(input, publication), afterCommit: a.publish(publication)})
}

// pendingPublication carries the transfer a write created to the step after
// its commit; a replayed write creates nothing and publishes nothing.
type pendingPublication struct {
	transferID *transfer.ID
}

func (a apiHandlers) applyPixOut(input application.PixOutInput, publication *pendingPublication) applyFunc {
	return func(ctx context.Context, stores application.Stores, operationID ledger.OperationID) (idempotency.StoredResponse, error) {
		result, err := a.useCases.Pix.Create.Apply(ctx, stores, operationID, input)
		if err != nil {
			return idempotency.StoredResponse{}, err
		}

		publication.transferID = &result.TransferID

		return storedJSON(http.StatusAccepted, pixOutResponse{TransferID: result.TransferID.String(), EndToEndID: result.EndToEndID, Status: result.Status, CreatedAt: result.CreatedAt.UTC()})
	}
}

func (a apiHandlers) publish(publication *pendingPublication) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		if publication.transferID == nil {
			return nil
		}

		return application.PublishAfterCommit(ctx, a.useCases.Pix.Publisher, *publication.transferID)
	}
}

func pixOutInputOf(r *http.Request, body []byte) (application.PixOutInput, error) {
	var request pixOutRequest

	err := decodeBody(body, &request)
	if err != nil {
		return application.PixOutInput{}, err
	}

	accountID, err := ledger.ParseAccountID(request.AccountID)
	if err != nil {
		return application.PixOutInput{}, err
	}

	amount, err := money.NewAmount(money.Cents(request.AmountCents))
	if err != nil {
		return application.PixOutInput{}, err
	}

	destination, err := transfer.ParseDestinationKey(request.DestinationKey)
	if err != nil {
		return application.PixOutInput{}, fmt.Errorf("%w: %w", errValidation, err)
	}

	return application.PixOutInput{AccountID: accountID, Amount: amount, DestinationKey: destination, Write: writeContextOf(r)}, nil
}

type transitionResponse struct {
	Seq       int              `json:"seq"`
	From      *transfer.Status `json:"from"`
	To        transfer.Status  `json:"to"`
	Cause     string           `json:"cause"`
	CreatedAt time.Time        `json:"created_at"`
}

type externalTransferResponse struct {
	TransferID  string               `json:"transfer_id"`
	EndToEndID  string               `json:"end_to_end_id"`
	AccountID   string               `json:"account_id"`
	AmountCents int64                `json:"amount_cents"`
	Status      transfer.Status      `json:"status"`
	StatusSeq   int                  `json:"status_seq"`
	FailureCode string               `json:"failure_code,omitempty"`
	Transitions []transitionResponse `json:"transitions"`
}

func (a apiHandlers) getPixOut(w http.ResponseWriter, r *http.Request) {
	id, err := transfer.ParseID(r.PathValue("id"))
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	view, err := a.useCases.Pix.Get.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	writeJSON(w, http.StatusOK, newExternalTransferResponse(view))
}

func newExternalTransferResponse(view application.ExternalTransferView) externalTransferResponse {
	external := view.Transfer
	transitions := make([]transitionResponse, 0, len(view.Transitions))

	for _, transition := range view.Transitions {
		transitions = append(transitions, transitionResponse{Seq: transition.Seq, From: transition.From, To: transition.To, Cause: transition.Cause, CreatedAt: transition.CreatedAt.UTC()})
	}

	return externalTransferResponse{
		TransferID:  external.ID.String(),
		EndToEndID:  external.EndToEndID,
		AccountID:   external.AccountID.String(),
		AmountCents: int64(external.Amount.Cents()),
		Status:      external.Status,
		StatusSeq:   external.StatusSeq,
		FailureCode: external.FailureCode,
		Transitions: transitions,
	}
}

type webhookEvent struct {
	EventID     string `json:"event_id"`
	Type        string `json:"type"`
	EndToEndID  string `json:"end_to_end_id"`
	AmountCents int64  `json:"amount_cents"`
	Sequence    int    `json:"sequence"`
	Reason      string `json:"reason"`
	AccountID   string `json:"destination_account_id"`
}

// webhook takes a partner event. Only a valid signature gets in; what it
// does with the event depends on the profile (naive: applies it right here).
func (a apiHandlers) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(w, r)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	isSigned := verifySignature(r.Header.Get(signatureHeader), body, a.useCases.Pix.WebhookSecret, clock.Now())
	if !isSigned {
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: errorDetail{Code: codeValidationFailed, Message: errBadSignature.Error(), CorrelationID: correlationIDFrom(r.Context())}})

		return
	}

	var event webhookEvent

	err = json.Unmarshal(body, &event)
	if err != nil {
		writeError(w, r, a.logger, fmt.Errorf("%w: webhook body must be a JSON event", errValidation))

		return
	}

	err = a.useCases.Pix.Webhook.Apply(r.Context(), application.PartnerEvent{ID: event.EventID, Type: event.Type, EndToEndID: event.EndToEndID, AmountCents: money.Cents(event.AmountCents), Sequence: event.Sequence, Reason: event.Reason, AccountID: event.AccountID})

	isDuplicateCredit := errors.Is(err, application.ErrAlreadyCredited)
	if err != nil && !isDuplicateCredit {
		writeError(w, r, a.logger, err)

		return
	}

	// FP05: the event is taken in and the partner not yet answered; it will
	// deliver the event again (FC06).
	err = failpoint.Hit(r.Context(), failpoint.WebhookAfterReceiveBeforeAck)
	if err != nil {
		writeError(w, r, a.logger, err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"received": true})
}

// verifySignature checks t=<unix>,v1=<hex HMAC-SHA256(secret, t + "." + body)>
// in constant time, refusing signatures older than the tolerance.
func verifySignature(header string, body, secret []byte, now time.Time) bool {
	timestampText, signature, isFound := strings.Cut(strings.TrimPrefix(header, "t="), ",v1=")
	if !isFound || len(secret) == 0 {
		return false
	}

	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return false
	}

	age := now.Sub(time.Unix(timestamp, 0))
	if age > signatureTolerance || age < -signatureTolerance {
		return false
	}

	mac := hmac.New(sha256.New, secret)

	// Writing to an in-memory hash never fails.
	_, _ = mac.Write([]byte(timestampText + "."))
	_, _ = mac.Write(body)

	expected := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(signature))
}
