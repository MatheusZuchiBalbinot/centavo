// Package config loads and validates the process configuration at startup.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

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
	chaosTokenVariable  = "CENTAVO_CHAOS_TOKEN" //nolint:gosec // G101: the name of a variable, not a secret.
	failpointsVariable  = "CENTAVO_FAILPOINTS"
	chaosSeedVariable   = "CENTAVO_CHAOS_SEED"
	instanceVariable    = "CENTAVO_INSTANCE"
	// minChaosTokenLength rejects guessable tokens (32 bytes of hex is 64).
	minChaosTokenLength = 32
)

var (
	ErrMissingVariable        = errors.New("required environment variable is not set")
	ErrUnknownEnv             = errors.New("unknown CENTAVO_ENV")
	ErrChaosBuildInProduction = errors.New("the chaoslab build refuses to run with CENTAVO_ENV=production")
	ErrProductionBuildProfile = errors.New("the production build only runs the robust defense profile")
	ErrWeakChaosToken         = errors.New("the chaoslab build needs CENTAVO_CHAOS_TOKEN with at least 32 characters")
	ErrChaosConfigProduction  = errors.New("the production build refuses chaos configuration")
	ErrInvalidChaosSeed       = errors.New("CENTAVO_CHAOS_SEED must be a 64-bit number")
)

// LookupFunc reads one environment variable; os.LookupEnv satisfies it.
type LookupFunc func(key string) (string, bool)

// Config is the validated configuration shared by every bank role.
type Config struct {
	Env         Env
	Defenses    defenses.Profile
	DatabaseURL string
	Chaos       Chaos
}

// Chaos is the chaos configuration; only a chaoslab build accepts it.
type Chaos struct {
	Token         string
	FailpointSpec string
	Seed          uint64
	// Instance names this process in chaos events; Compose sets the service name.
	Instance string
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

	chaos, err := loadChaos(lookup)
	if err != nil {
		return Config{}, err
	}

	return Config{Env: env, Defenses: profile, DatabaseURL: databaseURL, Chaos: chaos}, nil
}

func loadChaos(lookup LookupFunc) (Chaos, error) {
	token, _ := lookup(chaosTokenVariable)
	spec, _ := lookup(failpointsVariable)
	instance, _ := lookup(instanceVariable)
	rawSeed, _ := lookup(chaosSeedVariable)

	seed, err := parseSeed(rawSeed)
	if err != nil {
		return Chaos{}, err
	}

	return Chaos{Token: token, FailpointSpec: spec, Seed: seed, Instance: instance}, nil
}

// parseSeed reads 0x5eed0142 or a decimal; an empty seed is zero.
func parseSeed(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}

	digits, isHex := strings.CutPrefix(strings.ToLower(raw), "0x")

	base := 10
	if isHex {
		base = 16
	}

	seed, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: got %q", ErrInvalidChaosSeed, raw)
	}

	return seed, nil
}

// ValidateBuild enforces safety locks 1, 2 and 4 of docs/chaos-bot.md, section
// 16: chaos code never runs in production, production never runs a naive
// variant or takes chaos configuration, and the chaos port has a real secret.
func ValidateBuild(cfg Config, isChaosBuild bool) error {
	isChaosBuildInProduction := isChaosBuild && cfg.Env == EnvProduction
	if isChaosBuildInProduction {
		return ErrChaosBuildInProduction
	}

	isWeakenedProductionBuild := !isChaosBuild && !cfg.Defenses.IsRobust()
	if isWeakenedProductionBuild {
		return fmt.Errorf("%w: got %q", ErrProductionBuildProfile, cfg.Defenses)
	}

	hasChaosConfig := cfg.Chaos.FailpointSpec != ""
	if !isChaosBuild && hasChaosConfig {
		return ErrChaosConfigProduction
	}

	hasWeakToken := isChaosBuild && len(cfg.Chaos.Token) < minChaosTokenLength
	if hasWeakToken {
		return ErrWeakChaosToken
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
