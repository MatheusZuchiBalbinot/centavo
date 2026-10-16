// Package config loads and validates the partner configuration at startup.
package config

import (
	"errors"
	"fmt"
)

// Env is the deployment environment the process declares it runs in.
type Env string

const (
	EnvLocal      Env = "local"
	EnvChaoslab   Env = "chaoslab"
	EnvProduction Env = "production"
)

const envVariable = "CENTAVO_ENV"

var (
	ErrMissingVariable        = errors.New("required environment variable is not set")
	ErrUnknownEnv             = errors.New("unknown CENTAVO_ENV")
	ErrChaosBuildInProduction = errors.New("the chaoslab build refuses to run with CENTAVO_ENV=production")
)

// LookupFunc reads one environment variable; os.LookupEnv satisfies it.
type LookupFunc func(key string) (string, bool)

// Config is the validated partner configuration.
type Config struct {
	Env Env
}

// Load reads the configuration from the environment.
func Load(lookup LookupFunc) (Config, error) {
	raw, isSet := lookup(envVariable)

	isMissing := !isSet || raw == ""
	if isMissing {
		return Config{}, fmt.Errorf("%w: %s", ErrMissingVariable, envVariable)
	}

	env := Env(raw)

	switch env {
	case EnvLocal, EnvChaoslab, EnvProduction:
		return Config{Env: env}, nil
	default:
		return Config{}, fmt.Errorf("%w: %q", ErrUnknownEnv, raw)
	}
}

// ValidateBuild enforces safety lock 2 of docs/chaos-bot.md, section 16: the
// lying modes of a chaoslab build never run in production.
func ValidateBuild(cfg Config, isChaosBuild bool) error {
	isChaosBuildInProduction := isChaosBuild && cfg.Env == EnvProduction
	if isChaosBuildInProduction {
		return ErrChaosBuildInProduction
	}

	return nil
}
