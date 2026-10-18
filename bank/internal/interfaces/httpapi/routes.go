package httpapi

// Endpoints of the client API. The writes double as the endpoint an
// idempotency key is bound to, so renaming one changes every stored hash.
const (
	accountsEndpoint         = "/v1/accounts"
	accountEndpoint          = "/v1/accounts/{id}"
	statementEndpoint        = "/v1/accounts/{id}/statement"
	depositsEndpoint         = "/v1/deposits"
	transfersEndpoint        = "/v1/transfers"
	resolveEndpoint          = "/v1/operations/{key}/resolve"
	pixOutEndpoint           = "/v1/external-transfers"
	externalTransferEndpoint = "/v1/external-transfers/{id}"
	partnerWebhookEndpoint   = "/webhooks/partner"
)
