// Package config loads and validates the process configuration at startup.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"centavo/bank/internal/platform/defenses"
)

// The bases a seed may be written in.
const (
	decimalBase = 10
	hexBase     = 16
)

// Env is the deployment environment the process declares it runs in.
type Env string

// The environments the bank knows.
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
	partnerURLVariable  = "PARTNER_BASE_URL"
	participantVariable = "PARTNER_PARTICIPANT_ID"
	webhookSecretVar    = "PARTNER_WEBHOOK_SECRET"
	// minChaosTokenLength rejects guessable tokens (32 bytes of hex is 64).
	minChaosTokenLength = 32
)

// Errors of reading the configuration.
var (
	ErrMissingVariable        = errors.New("required environment variable is not set")
	ErrUnknownEnv             = errors.New("unknown CENTAVO_ENV")
	ErrChaosBuildInProduction = errors.New("the chaoslab build refuses to run with CENTAVO_ENV=production")
	ErrProductionBuildProfile = errors.New("the production build only runs the robust defense profile")
	ErrWeakChaosToken         = errors.New("the chaoslab build needs CENTAVO_CHAOS_TOKEN with at least 32 characters")
	ErrChaosConfigProduction  = errors.New("the production build refuses chaos configuration")
	ErrInvalidChaosSeed       = errors.New("CENTAVO_CHAOS_SEED must be a 64-bit number")
	ErrWeakWebhookSecret      = errors.New("the api role needs PARTNER_WEBHOOK_SECRET with at least 32 characters")
	ErrMissingPartner         = errors.New("the worker role needs PARTNER_BASE_URL and PARTNER_PARTICIPANT_ID")
)

// LookupFunc reads one environment variable; os.LookupEnv satisfies it.
type LookupFunc func(key string) (string, bool)

// Config is the validated configuration shared by every bank role.
type Config struct {
	Env         Env
	Defenses    defenses.Profile
	DatabaseURL string
	Chaos       Chaos
	Partner     Partner
}

// Partner is how the bank reaches the PIX partner and checks its webhooks.
type Partner struct {
	BaseURL       string
	ParticipantID string
	WebhookSecret string
}

// RequireWebhookSecret is the api role's check: unsigned webhooks never get in.
func (p Partner) RequireWebhookSecret() error {
	if len(p.WebhookSecret) < minChaosTokenLength {
		return ErrWeakWebhookSecret
	}

	return nil
}

// RequireEndpoint is the worker role's check: the relay must know the partner.
func (p Partner) RequireEndpoint() error {
	isPartnerMissing := p.BaseURL == "" || p.ParticipantID == ""
	if isPartnerMissing {
		return ErrMissingPartner
	}

	return nil
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

	baseURL, _ := lookup(partnerURLVariable)
	participant, _ := lookup(participantVariable)
	secret, _ := lookup(webhookSecretVar)
	partner := Partner{BaseURL: baseURL, ParticipantID: participant, WebhookSecret: secret}

	return Config{Env: env, Defenses: profile, DatabaseURL: databaseURL, Chaos: chaos, Partner: partner}, nil
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

	base := decimalBase
	if isHex {
		base = hexBase
	}

	seed, err := strconv.ParseUint(digits, base, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: got %q", ErrInvalidChaosSeed, raw)
	}

	return seed, nil
}

// ValidateBuild enforces the safety rule that chaos code exists only in
// chaoslab builds: chaos code never runs in production, production never runs a
// naive variant or takes chaos configuration, and the chaos port has a real
// secret.
func ValidateBuild(configuration Config, isChaosBuild bool) error {
	isChaosBuildInProduction := isChaosBuild && configuration.Env == EnvProduction
	if isChaosBuildInProduction {
		return ErrChaosBuildInProduction
	}

	isWeakenedProductionBuild := !isChaosBuild && !configuration.Defenses.IsRobust()
	if isWeakenedProductionBuild {
		return fmt.Errorf("%w: got %q", ErrProductionBuildProfile, configuration.Defenses)
	}

	hasChaosConfig := configuration.Chaos.FailpointSpec != ""
	if !isChaosBuild && hasChaosConfig {
		return ErrChaosConfigProduction
	}

	hasWeakToken := isChaosBuild && len(configuration.Chaos.Token) < minChaosTokenLength
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
