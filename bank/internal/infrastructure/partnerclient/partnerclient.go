// Package partnerclient calls the partner's PIX API (docs/architecture.md,
// section 4.4). It is the only place that knows the partner's HTTP contract.
package partnerclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"centavo/bank/internal/application"

	"centavo/bank/internal/domain/transfer"
)

// attemptTimeout is the partner call's timeout (docs/architecture.md, section 7).
const attemptTimeout = 2 * time.Second

const maxResponseBytes = 64 << 10

// Client sends PIX to the partner.
type Client struct {
	baseURL       string
	participantID string
	http          *http.Client
}

// New builds a client for one participant.
func New(baseURL, participantID string) Client {
	return Client{baseURL: baseURL, participantID: participantID, http: &http.Client{Timeout: attemptTimeout}}
}

type submitRequest struct {
	EndToEndID     transfer.EndToEndID `json:"end_to_end_id"`
	ParticipantID  string              `json:"participant_id"`
	AmountCents    int64               `json:"amount_cents"`
	DestinationKey string              `json:"destination_key"`
}

// Submit sends one PIX once. 202 and 200 are accepted, 422 is a definitive
// refusal, anything else (timeout, 5xx, a broken connection) is unknown:
// the partner may have processed it.
func (c Client) Submit(ctx context.Context, submission application.PartnerSubmission) application.PartnerAnswer {
	body, err := json.Marshal(submitRequest{EndToEndID: submission.EndToEndID, ParticipantID: c.participantID, AmountCents: int64(submission.AmountCents), DestinationKey: submission.DestinationKey})
	if err != nil {
		return application.PartnerUnknown
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/transfers", bytes.NewReader(body))
	if err != nil {
		return application.PartnerUnknown
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Correlation-Id", submission.CorrelationID)

	response, err := c.http.Do(request)
	if err != nil {
		return application.PartnerUnknown
	}
	defer response.Body.Close()

	// Drained so the connection can be reused; the status code is the answer.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))

	return answerOf(response.StatusCode)
}

func answerOf(status int) application.PartnerAnswer {
	switch status {
	case http.StatusAccepted, http.StatusOK:
		return application.PartnerAccepted
	case http.StatusUnprocessableEntity:
		return application.PartnerRejected
	default:
		return application.PartnerUnknown
	}
}
