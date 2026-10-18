package config_test

import (
	"errors"
	"strings"
	"testing"

	"centavo/bank/internal/platform/config"
	"centavo/bank/internal/platform/defenses"
)

const testDatabaseURL = "postgres://bank_app@localhost:5432/bank_robust"

func TestLoad_ValidEnvironment(t *testing.T) {
	vars := map[string]string{"CENTAVO_ENV": "chaoslab", "CENTAVO_DEFENSES": "robust,-outbox", "DATABASE_URL": testDatabaseURL}
	lookup := lookupFrom(vars)

	configuration, err := config.Load(lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	isExpectedConfig := configuration.Env == config.EnvChaoslab && configuration.DatabaseURL == testDatabaseURL && !configuration.Defenses.IsEnabled(defenses.Outbox)
	if !isExpectedConfig {
		t.Fatalf("unexpected config: %+v", configuration)
	}
}

func TestLoad_DefensesDefaultToRobust(t *testing.T) {
	vars := map[string]string{"CENTAVO_ENV": "local", "DATABASE_URL": testDatabaseURL}
	lookup := lookupFrom(vars)

	configuration, err := config.Load(lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !configuration.Defenses.IsRobust() {
		t.Fatalf("defenses = %s, want robust", configuration.Defenses)
	}
}

func TestLoad_InvalidEnvironment(t *testing.T) {
	testCases := []struct {
		name    string
		vars    map[string]string
		wantErr error
	}{
		{name: "missing env", vars: map[string]string{"DATABASE_URL": testDatabaseURL}, wantErr: config.ErrMissingVariable},
		{name: "unknown env", vars: map[string]string{"CENTAVO_ENV": "staging", "DATABASE_URL": testDatabaseURL}, wantErr: config.ErrUnknownEnv},
		{name: "missing database url", vars: map[string]string{"CENTAVO_ENV": "local"}, wantErr: config.ErrMissingVariable},
		{name: "empty database url", vars: map[string]string{"CENTAVO_ENV": "local", "DATABASE_URL": ""}, wantErr: config.ErrMissingVariable},
		{name: "invalid profile", vars: map[string]string{"CENTAVO_ENV": "local", "CENTAVO_DEFENSES": "robust,-nope", "DATABASE_URL": testDatabaseURL}, wantErr: defenses.ErrUnknownDefense},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			lookup := lookupFrom(testCase.vars)

			_, err := config.Load(lookup)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestValidateBuild(t *testing.T) {
	naive, err := defenses.ParseProfile("naive")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	strongToken := config.Chaos{Token: strings.Repeat("t", 32)}
	weakToken := config.Chaos{Token: "short"}
	productionChaos := config.Chaos{FailpointSpec: "api.after_commit_before_response=kill"}

	testCases := []struct {
		name         string
		env          config.Env
		profile      defenses.Profile
		chaos        config.Chaos
		isChaosBuild bool
		wantErr      error
	}{
		{name: "production build, robust, production", env: config.EnvProduction, profile: defenses.Robust()},
		{name: "production build, robust, local", env: config.EnvLocal, profile: defenses.Robust()},
		{name: "production build refuses naive", env: config.EnvLocal, profile: naive, wantErr: config.ErrProductionBuildProfile},
		{name: "chaos build runs naive in chaoslab", env: config.EnvChaoslab, profile: naive, chaos: strongToken, isChaosBuild: true},
		{name: "chaos build refuses production", env: config.EnvProduction, profile: defenses.Robust(), chaos: strongToken, isChaosBuild: true, wantErr: config.ErrChaosBuildInProduction},
		{name: "chaos build refuses a weak token", env: config.EnvChaoslab, profile: defenses.Robust(), chaos: weakToken, isChaosBuild: true, wantErr: config.ErrWeakChaosToken},
		{name: "production build refuses failpoints", env: config.EnvLocal, profile: defenses.Robust(), chaos: productionChaos, wantErr: config.ErrChaosConfigProduction},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configuration := config.Config{Env: testCase.env, Defenses: testCase.profile, DatabaseURL: testDatabaseURL, Chaos: testCase.chaos}

			err := config.ValidateBuild(configuration, testCase.isChaosBuild)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestLoad_ReadsTheChaosSeed(t *testing.T) {
	vars := map[string]string{"CENTAVO_ENV": "chaoslab", "DATABASE_URL": testDatabaseURL, "CENTAVO_CHAOS_SEED": "0x5eed0142"}

	configuration, err := config.Load(lookupFrom(vars))
	if err != nil {
		t.Fatalf("got %+v, %v", configuration.Chaos, err)
	}

	if configuration.Chaos.Seed != 0x5eed0142 {
		t.Fatalf("got %+v, %v", configuration.Chaos, err)
	}

	vars["CENTAVO_CHAOS_SEED"] = "seed"

	_, err = config.Load(lookupFrom(vars))
	if !errors.Is(err, config.ErrInvalidChaosSeed) {
		t.Fatalf("invalid seed: got %v", err)
	}
}

func lookupFrom(vars map[string]string) config.LookupFunc {
	return func(key string) (string, bool) {
		value, isSet := vars[key]

		return value, isSet
	}
}
