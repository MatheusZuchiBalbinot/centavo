package partnerclient

import "net/url"

// Paths of the partner's PIX API.
const (
	transfersPath  = "/v1/transfers"
	statementsPath = "/v1/statements"
)

// transferPath is one transfer's status, by its end_to_end_id.
func transferPath(endToEndID string) string {
	return transfersPath + "/" + url.PathEscape(endToEndID)
}
