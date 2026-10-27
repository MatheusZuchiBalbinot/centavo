package dashboard

const (
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

func reportURLOf(round string) string {
	return "/rounds/" + round + "/" + reportMDFile
}
