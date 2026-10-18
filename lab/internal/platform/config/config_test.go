package config_test

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"centavo/lab/internal/platform/config"
)

func TestLoadRun(t *testing.T) {
	valid := map[string]string{"BANK_API_URL": "http://toxiproxy:28080", "ARTIFACT_STORE": "file:///data/rounds/", "AUDITOR_DATABASE_URL": "postgres://bank_auditor:secret@postgres:5432/bank_robust", "CENTAVO_CHAOS_TOKEN": strings.Repeat("t", 64), "DOCKER_HOST": "tcp://docker-proxy:2375", "CENTAVO_COMPOSE_PROJECT": "centavo", "BANK_CONTROL_URL": "http://bank-api:8080", "PARTNER_ADMIN_URL": "http://partner:9092", "PARTNER_PARTICIPANT_ID": "centavo-robust"}

	got, err := config.LoadRun(lookupFrom(valid))
	if err != nil {
		t.Fatal(err)
	}

	isUnexpected := got.BankURL != "http://toxiproxy:28080" || got.RoundsDir != "/data/rounds" || got.AuditorURL != valid["AUDITOR_DATABASE_URL"]
	if isUnexpected {
		t.Fatalf("got %+v", got)
	}

	cases := []struct {
		name  string
		key   string
		value string
		want  error
	}{
		{name: "missing bank url", key: "BANK_API_URL", value: "", want: config.ErrMissingVariable},
		{name: "relative bank url", key: "BANK_API_URL", value: "toxiproxy:28080", want: config.ErrInvalidBankURL},
		{name: "javascript bank url", key: "BANK_API_URL", value: "javascript:alert(1)", want: config.ErrInvalidBankURL},
		{name: "s3 store", key: "ARTIFACT_STORE", value: "s3://bucket/rounds", want: config.ErrUnsupportedStore},
		{name: "relative file store", key: "ARTIFACT_STORE", value: "file://data/rounds", want: config.ErrUnsupportedStore},
		{name: "missing store", key: "ARTIFACT_STORE", value: "", want: config.ErrMissingVariable},
		{name: "missing auditor", key: "AUDITOR_DATABASE_URL", value: "", want: config.ErrMissingVariable},
		{name: "weak chaos token", key: "CENTAVO_CHAOS_TOKEN", value: "short", want: config.ErrWeakChaosToken},
		{name: "missing docker host", key: "DOCKER_HOST", value: "", want: config.ErrMissingVariable},
		{name: "auditor is not postgres", key: "AUDITOR_DATABASE_URL", value: "mysql://auditor@db/bank", want: config.ErrInvalidAuditor},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			values := maps.Clone(valid)
			values[tc.key] = tc.value

			_, err := config.LoadRun(lookupFrom(values))
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func lookupFrom(values map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		value, isSet := values[key]

		return value, isSet
	}
}
