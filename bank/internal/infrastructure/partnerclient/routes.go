package partnerclient

import "net/url"

const (
	transfersPath  = "/v1/transfers"
	statementsPath = "/v1/statements"
)

func transferPath(endToEndID string) string {
	return transfersPath + "/" + url.PathEscape(endToEndID)
}
