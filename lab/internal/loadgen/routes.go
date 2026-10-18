package loadgen

import "net/url"

// Paths of the bank's client API and of the partner's admin port.
const (
	accountsPath          = "/v1/accounts"
	depositsPath          = "/v1/deposits"
	transfersPath         = "/v1/transfers"
	externalTransfersPath = "/v1/external-transfers"
	operationsPath        = "/v1/operations"
	incomingPath          = "/internal/incoming"
)

func accountPath(id string) string {
	return accountsPath + "/" + url.PathEscape(id)
}

func statementPath(id string) string {
	return accountPath(id) + "/statement"
}

func externalTransferPath(id string) string {
	return externalTransfersPath + "/" + url.PathEscape(id)
}

func resolvePath(key string) string {
	return operationsPath + "/" + url.PathEscape(key) + "/resolve"
}
