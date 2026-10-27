// Package attacks is the menu of the panel: each attack is a family of named
// scenarios, and starting one picks a scenario of the family for the round.
// The names people read live in the panel's message catalog, keyed by ID.
package attacks

import (
	"errors"
	"slices"
	"time"
)

type ID string

const (
	WorkersDown      ID = "workers-down"
	Network          ID = "network"
	LyingPartner     ID = "lying-partner"
	DatabaseFailover ID = "database-failover"
	Clock            ID = "clock"
	Everything       ID = "everything"
	Calm             ID = "calm"
)

type Attack struct {
	ID        ID
	Scenarios []string
}

var (
	ErrUnknown  = errors.New("unknown attack")
	ErrDuration = errors.New("duration not offered")
)

// Long enough for the faults to land, short enough that a round with its
// cure and check stays around ten minutes.
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

// Everything draws from every family that injects faults.
var menu = []Attack{
	{ID: WorkersDown, Scenarios: workersDown},
	{ID: Network, Scenarios: network},
	{ID: LyingPartner, Scenarios: lyingPartner},
	{ID: DatabaseFailover, Scenarios: databaseFailover},
	{ID: Clock, Scenarios: clock},
	{ID: Everything, Scenarios: slices.Concat(workersDown, network, lyingPartner, databaseFailover, clock)},
	{ID: Calm, Scenarios: calm},
}

func Menu() []Attack {
	copied := make([]Attack, len(menu))

	for index, attack := range menu {
		copied[index] = Attack{ID: attack.ID, Scenarios: slices.Clone(attack.Scenarios)}
	}

	return copied
}

func Durations() []time.Duration {
	return slices.Clone(durations)
}

// index must return a number in [0, n).
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
