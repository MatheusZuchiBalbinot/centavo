// Package chaos wires the chaos controls of a chaoslab build. The
// production build compiles a version that does nothing.
package chaos

// Addr is the chaos control port; it is never published outside the Compose
// network.
const Addr = ":9091"

const EventsDir = "/lab/events"

type Config struct {
	Token         string
	FailpointSpec string
	Seed          uint64
	Service       string
	Instance      string
}
