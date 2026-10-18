package partnerclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
)

// errUnexpectedStatus is an answer that is neither a status nor not found.
var errUnexpectedStatus = errors.New("partner status query failed")

type statusResponse struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// Status asks the partner for a PIX's authoritative status. 404 is the
// partner saying it never received it; anything but 200 or 404 is an error,
// never a status.
func (c Client) Status(ctx context.Context, endToEndID transfer.EndToEndID) (application.AuthoritativeStatus, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/transfers/"+url.PathEscape(endToEndID.String()), http.NoBody)
	if err != nil {
		return application.AuthoritativeStatus{}, fmt.Errorf("building the status request: %w", err)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return application.AuthoritativeStatus{}, fmt.Errorf("requesting the status: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))

		return application.AuthoritativeStatus{Status: transfer.PartnerNotFound}, nil
	}

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))

		return application.AuthoritativeStatus{}, fmt.Errorf("%w: HTTP %d", errUnexpectedStatus, response.StatusCode)
	}

	var body statusResponse

	err = json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&body)
	if err != nil {
		return application.AuthoritativeStatus{}, fmt.Errorf("%w: %w", errUnexpectedStatus, err)
	}

	status, err := partnerStatusOf(body.Status)
	if err != nil {
		return application.AuthoritativeStatus{}, err
	}

	return application.AuthoritativeStatus{Status: status, Reason: body.Reason}, nil
}

func partnerStatusOf(raw string) (transfer.PartnerStatus, error) {
	status := transfer.PartnerStatus(raw)

	switch status {
	case transfer.PartnerProcessing, transfer.PartnerSettled, transfer.PartnerRejected, transfer.PartnerReturned:
		return status, nil
	case transfer.PartnerNotFound:
		return "", fmt.Errorf("%w: not_found with HTTP 200", errUnexpectedStatus)
	default:
		return "", fmt.Errorf("%w: unknown status %q", errUnexpectedStatus, raw)
	}
}
