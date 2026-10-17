package partnerclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/money"
)

// maxStatementBytes bounds one page of the statement: up to 1000 entries.
const maxStatementBytes = 1 << 20

type statementPage struct {
	Entries []statementLine `json:"entries"`
}

type statementLine struct {
	Seq                  int64  `json:"seq"`
	Kind                 string `json:"kind"`
	EndToEndID           string `json:"end_to_end_id"`
	AmountCents          int64  `json:"amount_cents"`
	DestinationAccountID string `json:"destination_account_id"`
	CorrelationID        string `json:"correlation_id"`
}

// Statement reads the participant's statement after a sequence number.
func (c Client) Statement(ctx context.Context, afterSeq int64, limit int) ([]application.StatementEntry, error) {
	query := url.Values{"participant_id": {c.participantID}, "after_seq": {strconv.FormatInt(afterSeq, 10)}, "limit": {strconv.Itoa(limit)}}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/statements?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes))

		return nil, fmt.Errorf("%w: statement HTTP %d", errUnexpectedStatus, response.StatusCode)
	}

	var page statementPage

	err = json.NewDecoder(io.LimitReader(response.Body, maxStatementBytes)).Decode(&page)
	if err != nil {
		return nil, fmt.Errorf("%w: statement: %w", errUnexpectedStatus, err)
	}

	entries := make([]application.StatementEntry, 0, len(page.Entries))

	for _, line := range page.Entries {
		entry := application.StatementEntry{Seq: line.Seq, Kind: application.StatementKind(line.Kind), EndToEndID: line.EndToEndID, AmountCents: money.Cents(line.AmountCents), AccountID: line.DestinationAccountID, CorrelationID: line.CorrelationID}
		entries = append(entries, entry)
	}

	return entries, nil
}
