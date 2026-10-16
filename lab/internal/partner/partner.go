// Package partner reads the partner's admin port: its truth and how many
// webhooks it still has to deliver. The lab keeps its own types; it never
// imports the partner.
package partner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	callTimeout   = 30 * time.Second
	maxTruthBytes = 512 << 20
)

// Client reads one participant's truth.
type Client struct {
	baseURL       string
	participantID string
	http          *http.Client
}

// New builds a client for the partner's admin port.
func New(baseURL, participantID string) Client {
	return Client{baseURL: baseURL, participantID: participantID, http: &http.Client{Timeout: callTimeout}}
}

// Truth returns GET /internal/truth as the partner sent it, for truth.json.
func (c Client) Truth(ctx context.Context) ([]byte, error) {
	return c.get(ctx, "/internal/truth?participant_id="+url.QueryEscape(c.participantID))
}

// PendingWebhooks counts the deliveries the partner has not finished.
func (c Client) PendingWebhooks(ctx context.Context) (int, error) {
	body, err := c.get(ctx, "/internal/pending")
	if err != nil {
		return 0, err
	}

	var pending struct {
		Count int `json:"pending_deliveries"`
	}

	err = json.Unmarshal(body, &pending)
	if err != nil {
		return 0, fmt.Errorf("reading pending webhooks: %w", err)
	}

	return pending.Count, nil
}

func (c Client) get(ctx context.Context, path string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("partner %s: %w", path, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxTruthBytes))
	if err != nil {
		return nil, fmt.Errorf("partner %s: %w", path, err)
	}

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("partner %s answered %d", path, response.StatusCode)
	}

	return body, nil
}
