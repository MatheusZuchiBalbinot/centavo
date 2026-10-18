package httpapi

// Routes of the client port and of the admin port.
const (
	transfersRoute  = "/v1/transfers"
	transferRoute   = "/v1/transfers/{e2e}"
	statementsRoute = "/v1/statements"
	incomingRoute   = "/internal/incoming"
	truthRoute      = "/internal/truth"
	pendingRoute    = "/internal/pending"
	chaosPrefix     = "/chaos/"
)
