package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	EnvLocal      Env = "local"
	EnvChaoslab   Env = "chaoslab"
	EnvProduction Env = "production"
)

const (
	envVariable          = "CENTAVO_ENV"
	dbPathVariable       = "PARTNER_DB_PATH"
	participantsVariable = "PARTNER_PARTICIPANTS"
	secretVariable       = "PARTNER_WEBHOOK_SECRET"
	chaosTokenVariable   = "CENTAVO_CHAOS_TOKEN" //nolint:gosec // G101: the name of a variable, not a secret.
	// minSecretLength rejects guessable secrets (32 random bytes in hex is 64).
	minSecretLength = 32
)

var participantPattern = regexp.MustCompile(`^[a-z0-9-]{3,40}$`)

var (
	ErrMissingVariable        = errors.New("required environment variable is not set")
	ErrUnknownEnv             = errors.New("unknown CENTAVO_ENV")
	ErrChaosBuildInProduction = errors.New("the chaoslab build refuses to run with CENTAVO_ENV=production")
	ErrInvalidParticipants    = errors.New("PARTNER_PARTICIPANTS must be id=http(s)://webhook-url pairs separated by commas")
	ErrWeakSecret             = errors.New("PARTNER_WEBHOOK_SECRET needs at least 32 characters")
	ErrWeakChaosToken         = errors.New("the chaoslab build needs CENTAVO_CHAOS_TOKEN with at least 32 characters")
)

type Env string

type LookupFunc func(key string) (string, bool)

type Participant struct {
	ID         string
	WebhookURL string
}

type Config struct {
	Env           Env
	DBPath        string
	Participants  []Participant
	WebhookSecret string
	ChaosToken    string
}

func Load(lookup LookupFunc) (Config, error) {
	rawEnv, err := require(lookup, envVariable)
	if err != nil {
		return Config{}, err
	}

	env, err := parseEnv(rawEnv)
	if err != nil {
		return Config{}, err
	}

	dbPath, err := require(lookup, dbPathVariable)
	if err != nil {
		return Config{}, err
	}

	rawParticipants, err := require(lookup, participantsVariable)
	if err != nil {
		return Config{}, err
	}

	participants, err := parseParticipants(rawParticipants)
	if err != nil {
		return Config{}, err
	}

	secret, _ := lookup(secretVariable)
	if len(secret) < minSecretLength {
		return Config{}, ErrWeakSecret
	}

	token, _ := lookup(chaosTokenVariable)

	err = refuseExampleSecrets(secret, token)
	if err != nil {
		return Config{}, err
	}

	return Config{Env: env, DBPath: dbPath, Participants: participants, WebhookSecret: secret, ChaosToken: token}, nil
}

// A chaoslab build refuses to run in production, and the chaos port needs a
// real secret.
func ValidateBuild(configuration Config, isChaosBuild bool) error {
	isChaosBuildInProduction := isChaosBuild && configuration.Env == EnvProduction
	if isChaosBuildInProduction {
		return ErrChaosBuildInProduction
	}

	hasWeakToken := isChaosBuild && len(configuration.ChaosToken) < minSecretLength
	if hasWeakToken {
		return ErrWeakChaosToken
	}

	return nil
}

func parseParticipants(raw string) ([]Participant, error) {
	var participants []Participant

	for pair := range strings.SplitSeq(raw, ",") {
		id, webhookURL, hasSeparator := strings.Cut(strings.TrimSpace(pair), "=")

		parsed, err := url.Parse(webhookURL)
		isHTTP := err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""

		isValid := hasSeparator && participantPattern.MatchString(id) && isHTTP
		if !isValid {
			return nil, fmt.Errorf("%w: got %q", ErrInvalidParticipants, pair)
		}

		participants = append(participants, Participant{ID: id, WebhookURL: webhookURL})
	}

	return participants, nil
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
