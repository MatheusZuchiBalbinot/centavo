package config_test

import (
	"errors"
	"testing"

	"centavo/partner/internal/platform/config"
)

func TestLoad(t *testing.T) {
	testCases := []struct {
		name    string
		vars    map[string]string
		wantEnv config.Env
		wantErr error
	}{
		{name: "chaoslab", vars: map[string]string{"CENTAVO_ENV": "chaoslab"}, wantEnv: config.EnvChaoslab},
		{name: "missing", vars: map[string]string{}, wantErr: config.ErrMissingVariable},
		{name: "empty", vars: map[string]string{"CENTAVO_ENV": ""}, wantErr: config.ErrMissingVariable},
		{name: "unknown", vars: map[string]string{"CENTAVO_ENV": "staging"}, wantErr: config.ErrUnknownEnv},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lookup := lookupFrom(testCase.vars)

			cfg, err := config.Load(lookup)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}

			if cfg.Env != testCase.wantEnv {
				t.Fatalf("env = %q, want %q", cfg.Env, testCase.wantEnv)
			}
		})
	}
}

func TestValidateBuild(t *testing.T) {
	testCases := []struct {
		name         string
		env          config.Env
		isChaosBuild bool
		wantErr      error
	}{
		{name: "production build in production", env: config.EnvProduction},
		{name: "chaos build in chaoslab", env: config.EnvChaoslab, isChaosBuild: true},
		{name: "chaos build refuses production", env: config.EnvProduction, isChaosBuild: true, wantErr: config.ErrChaosBuildInProduction},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := config.Config{Env: testCase.env}

			err := config.ValidateBuild(cfg, testCase.isChaosBuild)
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
