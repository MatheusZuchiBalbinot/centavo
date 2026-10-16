// Package chaos wires the chaos controls of a chaoslab build: the control
// server on :9091, the failpoint events and the chaos-kill command. The
// production build compiles a version that does nothing (lock 1 of
// docs/chaos-bot.md, section 16).
package chaos

// Addr is the chaos control port; it is never published outside the Compose
// network (lock 3).
const Addr = ":9091"

// EventsDir is the shared volume where fired failpoints are recorded.
const EventsDir = "/lab/events"

// Config is what the chaos controls need from the process configuration.
type Config struct {
	Token         string
	FailpointSpec string
	Seed          uint64
	Service       string
	Instance      string
}
