// Package attacks is the menu of the panel: each attack is a family of named
// scenarios, and starting one picks a scenario of the family for the round.
// The names people read live in the panel's message catalog, keyed by ID.
package attacks

import (
	"errors"
	"slices"
	"time"
)

// ID names an attack in the API and in the panel's messages.
type ID string

// The attacks on the menu.
const (
	WorkersDown      ID = "workers-down"
	Network          ID = "network"
	LyingPartner     ID = "lying-partner"
	DatabaseFailover ID = "database-failover"
	Clock            ID = "clock"
	Everything       ID = "everything"
	Calm             ID = "calm"
)

// Attack is one card of the menu.
type Attack struct {
	ID        ID
	Scenarios []string
}

var (
	// ErrUnknown means the attack is not on the menu.
	ErrUnknown = errors.New("unknown attack")
	// ErrDuration means the duration is not one the panel offers.
	ErrDuration = errors.New("duration not offered")
)

// durations are the attack lengths the panel offers: long enough for the
// faults to land, short enough that a round with its cure and check stays
// around ten minutes.
var durations = []time.Duration{3 * time.Minute, 5 * time.Minute, 10 * time.Minute}

var (
	workersDown = []string{
		"kill-relay-after-partner-call", "kill-after-commit", "kill-between-debit-credit", "kill-during-reconciliation",
		"lost-publish", "random-kills",
	}
	network      = []string{"network-partition", "cut-responses", "slow-network", "retry-storm", "duplicate-concurrent"}
	lyingPartner = []string{
		"partner-double-settle", "partner-returns", "partner-timeout-but-processed", "partner-outage", "webhook-chaos",
		"duplicate-webhooks", "webhook-redelivery", "webhook-blackhole", "webhook-blackhole-out",
	}
	databaseFailover = []string{"failover-sync", "failover-async", "postgres-crash", "postgres-freeze"}
	clock            = []string{"clock-skew", "zombie-reconciler"}
	calm             = []string{"smoke", "concurrency-hotspot"}
)

// menu is in the order the panel shows it; Everything draws from every
// family that injects faults.
var menu = []Attack{
	{ID: WorkersDown, Scenarios: workersDown},
	{ID: Network, Scenarios: network},
	{ID: LyingPartner, Scenarios: lyingPartner},
	{ID: DatabaseFailover, Scenarios: databaseFailover},
	{ID: Clock, Scenarios: clock},
	{ID: Everything, Scenarios: slices.Concat(workersDown, network, lyingPartner, databaseFailover, clock)},
	{ID: Calm, Scenarios: calm},
}

// Menu returns a copy of the attacks, in the panel's order.
func Menu() []Attack {
	copied := make([]Attack, len(menu))

	for index, attack := range menu {
		copied[index] = Attack{ID: attack.ID, Scenarios: slices.Clone(attack.Scenarios)}
	}

	return copied
}

// Durations returns the attack lengths the panel offers.
func Durations() []time.Duration {
	return slices.Clone(durations)
}

// Pick returns the scenario of a round of the attack; index chooses among
// the family's scenarios and must return a number in [0, n).
func Pick(id ID, duration time.Duration, index func(n int) int) (string, error) {
	isOffered := slices.Contains(durations, duration)
	if !isOffered {
		return "", ErrDuration
	}

	position := slices.IndexFunc(menu, func(attack Attack) bool { return attack.ID == id })
	if position < 0 {
		return "", ErrUnknown
	}

	scenarios := menu[position].Scenarios

	return scenarios[index(len(scenarios))], nil
}
