package dashboard

// Routes of the panel.
const (
	statusRoute = "/api/status"
	liveRoute   = "/api/live"
	replayRoute = "/api/replay/{round}"
	reportRoute = "/rounds/{round}/" + reportMDFile
	staticRoute = "/"
)

// reportURLOf is where the panel serves a round's report.
func reportURLOf(round string) string {
	return "/rounds/" + round + "/" + reportMDFile
}
