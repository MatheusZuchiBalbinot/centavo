package scenarios_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"centavo/lab/scenarios"
)

func TestEveryEmbeddedScenarioLoads(t *testing.T) {
	names := scenarios.Names()
	if len(names) == 0 {
		t.Fatal("no scenario embedded")
	}

	for _, name := range names {
		_, err := scenarios.Load(name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLoad_Smoke(t *testing.T) {
	smoke, err := scenarios.Load("smoke")
	if err != nil {
		t.Fatal(err)
	}

	isExpected := smoke.Duration == 5*time.Minute &&
		smoke.Workload.Profile == scenarios.ProfileLedgerOnly &&
		smoke.Workload.ExclusiveAccounts &&
		len(smoke.SHA256) == 64
	if !isExpected {
		t.Fatalf("smoke: %+v", smoke)
	}
}

func TestLoad_RejectsUnknownNamesAndPathTricks(t *testing.T) {
	for _, name := range []string{"does-not-exist", "../smoke", "smoke.yaml", ""} {
		_, err := scenarios.Load(name)
		if !errors.Is(err, scenarios.ErrUnknownScenario) {
			t.Errorf("%q: got %v", name, err)
		}
	}
}

func TestParse_RejectsInvalidScenarios(t *testing.T) {
	const valid = `name: demo
duration: 1m
workload:
  profile: ledger-only
  clients: 2
  rate_per_second: 5
  accounts: 4
  exclusive_accounts: true
  client_mix: {polite: 1.0}
`

	_, err := scenarios.Parse("demo", []byte(valid))
	if err != nil {
		t.Fatalf("valid scenario rejected: %v", err)
	}

	cases := []struct {
		name    string
		from    string
		to      string
		wantErr string
	}{
		{name: "unknown field", from: "duration: 1m", to: "duration: 1m\nfaults: []", wantErr: "faults"},
		{name: "wrong name", from: "name: demo", to: "name: other", wantErr: "declares name"},
		{name: "bad duration", from: "duration: 1m", to: "duration: soon", wantErr: "duration"},
		{name: "unknown profile", from: "ledger-only", to: "balanced", wantErr: "not implemented"},
		{name: "no clients", from: "clients: 2", to: "clients: 0", wantErr: "clients"},
		{name: "no rate", from: "rate_per_second: 5", to: "rate_per_second: 0", wantErr: "rate_per_second"},
		{name: "too few exclusive accounts", from: "accounts: 4", to: "accounts: 3", wantErr: "at least 4"},
		{name: "unknown client profile", from: "polite: 1.0", to: "duplicator: 1.0", wantErr: "duplicator"},
		{name: "zero weight", from: "polite: 1.0", to: "polite: 0", wantErr: "positive weight"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.Replace(valid, tc.from, tc.to, 1)

			_, err := scenarios.Parse("demo", []byte(content))

			isExpected := errors.Is(err, scenarios.ErrInvalidScenario) && strings.Contains(err.Error(), tc.wantErr)
			if !isExpected {
				t.Fatalf("got %v, want an error mentioning %q", err, tc.wantErr)
			}
		})
	}
}
