package scenarios_test

import (
	"testing"

	"centavo/lab/scenarios"
)

// FuzzParse: a scenario file is edited by hand, so the parser must refuse a
// broken one with an error, never a panic.
func FuzzParse(f *testing.F) {
	f.Add([]byte("name: smoke\nduration: 1m\nworkload:\n  profile: balanced\n  clients: 2\n  rate_per_second: 5\n  accounts: 4\n"))
	f.Add([]byte("faults:\n  - {id: f, kind: clock, targets: [bank-api]}\n"))
	f.Add([]byte("duration: -1m"))

	f.Fuzz(func(_ *testing.T, content []byte) {
		_, _ = scenarios.Parse("fuzz", content)
	})
}
