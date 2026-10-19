package dashboard

// Routes of the panel.
const (
	statusRoute  = "/api/status"
	liveRoute    = "/api/live"
	replayRoute  = "/api/replay/{round}"
	reportRoute  = "/rounds/{round}/" + reportMDFile
	staticRoute  = "/"
	apiPrefix    = "/api/"
	labRoute     = "/api/lab"
	stopRoute    = "/api/lab/stop"
	attacksRoute = "/api/attacks"
	roundsRoute  = "/api/rounds"
	eventsRoute  = "/api/rounds/{round}/events"
)

// reportURLOf is where the panel serves a round's report.
func reportURLOf(round string) string {
	return "/rounds/" + round + "/" + reportMDFile
}
