package attacks

import (
	"errors"
	"testing"
	"time"

	"centavo/lab/scenarios"
)

func TestMenu_EveryScenarioExistsAndAppearsOncePerAttack(t *testing.T) {
	for _, attack := range Menu() {
		if len(attack.Scenarios) == 0 {
			t.Fatalf("attack %s has no scenario", attack.ID)
		}

		seen := map[string]bool{}

		for _, name := range attack.Scenarios {
			_, err := scenarios.Load(name)
			if err != nil {
				t.Fatalf("attack %s: scenario %s: %v", attack.ID, name, err)
			}

			if seen[name] {
				t.Fatalf("attack %s lists %s twice", attack.ID, name)
			}

			seen[name] = true
		}
	}
}

func TestMenu_ReturnsACopyThePanelCannotChange(t *testing.T) {
	first := Menu()
	first[0].Scenarios[0] = "changed"

	second := Menu()
	if second[0].Scenarios[0] == "changed" {
		t.Fatal("Menu shares its slices with the catalog")
	}
}

func TestPick_ChoosesWithinTheFamily(t *testing.T) {
	last := func(n int) int { return n - 1 }

	scenario, err := Pick(Clock, 5*time.Minute, last)
	if err != nil {
		t.Fatal(err)
	}

	if scenario != "zombie-reconciler" {
		t.Fatalf("picked %s, want the last scenario of the clock family", scenario)
	}
}

func TestPick_RefusesWhatTheMenuDoesNotOffer(t *testing.T) {
	first := func(int) int { return 0 }

	_, err := Pick("drop-tables", 5*time.Minute, first)
	if !errors.Is(err, ErrUnknown) {
		t.Fatalf("unknown attack: %v, want ErrUnknown", err)
	}

	_, err = Pick(Network, 7*time.Minute, first)
	if !errors.Is(err, ErrDuration) {
		t.Fatalf("7 min: %v, want ErrDuration", err)
	}
}
