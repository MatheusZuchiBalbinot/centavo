package config_test

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"centavo/partner/internal/platform/config"
)

func validVars() map[string]string {
	participants := "centavo-robust=http://toxiproxy:18080/webhooks/partner, centavo-naive=http://toxiproxy:18083/webhooks/partner"

	return map[string]string{
		"CENTAVO_ENV": "chaoslab", "PARTNER_DB_PATH": "/data/truth.db", "PARTNER_PARTICIPANTS": participants,
		"PARTNER_WEBHOOK_SECRET": strings.Repeat("s", 64), "CENTAVO_CHAOS_TOKEN": strings.Repeat("t", 64),
	}
}

func TestLoad(t *testing.T) {
	configuration, err := config.Load(lookupFrom(validVars()))
	if err != nil {
		t.Fatal(err)
	}

	isLoaded := configuration.Env == config.EnvChaoslab && len(configuration.Participants) == 2 && configuration.Participants[1].ID == "centavo-naive"
	if !isLoaded {
		t.Fatalf("config: %+v", configuration)
	}

	testCases := []struct {
		name    string
		key     string
		value   string
		wantErr error
	}{
		{name: "missing env", key: "CENTAVO_ENV", value: "", wantErr: config.ErrMissingVariable},
		{name: "unknown env", key: "CENTAVO_ENV", value: "staging", wantErr: config.ErrUnknownEnv},
		{name: "missing database", key: "PARTNER_DB_PATH", value: "", wantErr: config.ErrMissingVariable},
		{name: "participant without url", key: "PARTNER_PARTICIPANTS", value: "centavo-robust", wantErr: config.ErrInvalidParticipants},
		{name: "javascript webhook", key: "PARTNER_PARTICIPANTS", value: "centavo-robust=javascript:alert(1)", wantErr: config.ErrInvalidParticipants},
		{name: "weak secret", key: "PARTNER_WEBHOOK_SECRET", value: "short", wantErr: config.ErrWeakSecret},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			vars := maps.Clone(validVars())
			vars[testCase.key] = testCase.value

			_, err := config.Load(lookupFrom(vars))
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestValidateBuild(t *testing.T) {
	strong := strings.Repeat("t", 32)

	testCases := []struct {
		name         string
		cfg          config.Config
		isChaosBuild bool
		wantErr      error
	}{
		{name: "production build in production", cfg: config.Config{Env: config.EnvProduction}},
		{name: "chaos build in chaoslab", cfg: config.Config{Env: config.EnvChaoslab, ChaosToken: strong}, isChaosBuild: true},
		{name: "chaos build refuses production", cfg: config.Config{Env: config.EnvProduction, ChaosToken: strong}, isChaosBuild: true, wantErr: config.ErrChaosBuildInProduction},
		{name: "chaos build refuses a weak token", cfg: config.Config{Env: config.EnvChaoslab, ChaosToken: "short"}, isChaosBuild: true, wantErr: config.ErrWeakChaosToken},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := config.ValidateBuild(testCase.cfg, testCase.isChaosBuild)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func lookupFrom(vars map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		value, isSet := vars[key]

		return value, isSet
	}
}
