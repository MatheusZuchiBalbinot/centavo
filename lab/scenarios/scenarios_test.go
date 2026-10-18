package scenarios_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"centavo/lab/scenarios"
)

func TestEveryEmbeddedScenarioLoads(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	smoke, err := scenarios.Load("smoke")
	if err != nil {
		t.Fatal(err)
	}

	isExpected := smoke.Duration == 5*time.Minute &&
		smoke.Workload.Profile == scenarios.ProfileLedgerOnly &&
		smoke.Workload.HasExclusiveAccounts &&
		len(smoke.SHA256) == 64
	if !isExpected {
		t.Fatalf("smoke: %+v", smoke)
	}
}

func TestLoad_RejectsUnknownNamesAndPathTricks(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"does-not-exist", "../smoke", "smoke.yaml", ""} {
		_, err := scenarios.Load(name)
		if !errors.Is(err, scenarios.ErrUnknownScenario) {
			t.Errorf("%q: got %v", name, err)
		}
	}
}

func TestParse_RejectsInvalidScenarios(t *testing.T) {
	t.Parallel()

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
		{name: "unknown field", from: "duration: 1m", to: "duration: 1m\nexpect: {}", wantErr: "expect"},
		{name: "wrong name", from: "name: demo", to: "name: other", wantErr: "declares name"},
		{name: "bad duration", from: "duration: 1m", to: "duration: soon", wantErr: "duration"},
		{name: "unknown profile", from: "ledger-only", to: "hotspot", wantErr: "not implemented"},
		{name: "no clients", from: "clients: 2", to: "clients: 0", wantErr: "clients"},
		{name: "no rate", from: "rate_per_second: 5", to: "rate_per_second: 0", wantErr: "rate_per_second"},
		{name: "too few exclusive accounts", from: "accounts: 4", to: "accounts: 3", wantErr: "at least 4"},
		{name: "unknown client profile", from: "polite: 1.0", to: "impatient: 1.0", wantErr: "impatient"},
		{name: "failpoint on the partner", from: "duration: 1m", to: "duration: 1m\nfaults:\n  - {id: f, kind: failpoint, targets: [partner], failpoint: api.after_commit_before_response, action: kill, probability: 0.1}", wantErr: "not allowed"},
		{name: "failpoint without probability", from: "duration: 1m", to: "duration: 1m\nfaults:\n  - {id: f, kind: failpoint, targets: [bank-api], failpoint: api.after_commit_before_response, action: kill}", wantErr: "probability"},
		{name: "unknown fault kind", from: "duration: 1m", to: "duration: 1m\nfaults:\n  - {id: f, kind: replica, targets: [bank-api]}", wantErr: "not implemented"},
		{name: "network fault on an unknown link", from: "duration: 1m", to: "duration: 1m\nfaults:\n  - {id: f, kind: network, proxies: [lab_pg], toxic: {type: latency}}", wantErr: "proxies"},
		{name: "network fault with an unknown toxic", from: "duration: 1m", to: "duration: 1m\nfaults:\n  - {id: f, kind: network, proxies: [api_pg], toxic: {type: lag}}", wantErr: "toxic type"},
		{name: "clock fault without offset", from: "duration: 1m", to: "duration: 1m\nfaults:\n  - {id: f, kind: clock, targets: [bank-api]}", wantErr: "offset"},
		{name: "repeated fault id", from: "duration: 1m", to: "duration: 1m\nfaults:\n  - {id: f, kind: process, targets: [bank-api], schedule: {type: poisson, mean_interval: 5s}}\n  - {id: f, kind: process, targets: [partner], schedule: {type: poisson, mean_interval: 5s}}", wantErr: "unique id"},
		{name: "too frequent kills", from: "duration: 1m", to: "duration: 1m\nfaults:\n  - {id: f, kind: process, targets: [bank-api], schedule: {type: poisson, mean_interval: 10ms}}", wantErr: "mean_interval"},
		{name: "zero weight", from: "polite: 1.0", to: "polite: 0", wantErr: "positive weight"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			content := strings.Replace(valid, tc.from, tc.to, 1)

			_, err := scenarios.Parse("demo", []byte(content))

			isExpected := errors.Is(err, scenarios.ErrInvalidScenario) && strings.Contains(err.Error(), tc.wantErr)
			if !isExpected {
				t.Fatalf("got %v, want an error mentioning %q", err, tc.wantErr)
			}
		})
	}
}
