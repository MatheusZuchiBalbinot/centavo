// Package config loads and validates the process configuration at startup.
package config

import (
	"errors"
	"fmt"

	"centavo/bank/internal/platform/defenses"
)

// Env is the deployment environment the process declares it runs in.
type Env string

const (
	EnvLocal      Env = "local"
	EnvChaoslab   Env = "chaoslab"
	EnvProduction Env = "production"
)

const (
	envVariable         = "CENTAVO_ENV"
	defensesVariable    = "CENTAVO_DEFENSES"
	databaseURLVariable = "DATABASE_URL"
)

var (
	ErrMissingVariable        = errors.New("required environment variable is not set")
	ErrUnknownEnv             = errors.New("unknown CENTAVO_ENV")
	ErrChaosBuildInProduction = errors.New("the chaoslab build refuses to run with CENTAVO_ENV=production")
	ErrProductionBuildProfile = errors.New("the production build only runs the robust defense profile")
)

// LookupFunc reads one environment variable; os.LookupEnv satisfies it.
type LookupFunc func(key string) (string, bool)

// Config is the validated configuration shared by every bank role.
type Config struct {
	Env         Env
	Defenses    defenses.Profile
	DatabaseURL string
}

// Load reads the configuration from the environment.
func Load(lookup LookupFunc) (Config, error) {
	rawEnv, err := require(lookup, envVariable)
	if err != nil {
		return Config{}, err
	}

	env, err := parseEnv(rawEnv)
	if err != nil {
		return Config{}, err
	}

	profile, err := loadProfile(lookup)
	if err != nil {
		return Config{}, err
	}

	databaseURL, err := require(lookup, databaseURLVariable)
	if err != nil {
		return Config{}, err
	}

	return Config{Env: env, Defenses: profile, DatabaseURL: databaseURL}, nil
}

// ValidateBuild enforces safety locks 1 and 2 of docs/chaos-bot.md, section 16:
// chaos code never runs in production, and production never runs a naive variant.
func ValidateBuild(cfg Config, isChaosBuild bool) error {
	isChaosBuildInProduction := isChaosBuild && cfg.Env == EnvProduction
	if isChaosBuildInProduction {
		return ErrChaosBuildInProduction
	}

	isWeakenedProductionBuild := !isChaosBuild && !cfg.Defenses.IsRobust()
	if isWeakenedProductionBuild {
		return fmt.Errorf("%w: got %q", ErrProductionBuildProfile, cfg.Defenses)
	}

	return nil
}

func loadProfile(lookup LookupFunc) (defenses.Profile, error) {
	raw, isSet := lookup(defensesVariable)
	if !isSet {
		return defenses.Robust(), nil
	}

	profile, err := defenses.ParseProfile(raw)
	if err != nil {
		return defenses.Profile{}, fmt.Errorf("%s: %w", defensesVariable, err)
	}

	return profile, nil
}

func parseEnv(raw string) (Env, error) {
	env := Env(raw)

	switch env {
	case EnvLocal, EnvChaoslab, EnvProduction:
		return env, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnknownEnv, raw)
	}
}

func require(lookup LookupFunc, key string) (string, error) {
	value, isSet := lookup(key)

	isMissing := !isSet || value == ""
	if isMissing {
		return "", fmt.Errorf("%w: %s", ErrMissingVariable, key)
	}

	return value, nil
}
