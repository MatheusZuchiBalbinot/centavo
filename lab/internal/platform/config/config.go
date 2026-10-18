// Package config loads and validates the lab configuration at startup.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
)

const (
	bankURLVariable       = "BANK_API_URL"
	controlURLVariable    = "BANK_CONTROL_URL"
	artifactStoreVariable = "ARTIFACT_STORE"
	auditorURLVariable    = "AUDITOR_DATABASE_URL"
	chaosTokenVariable    = "CENTAVO_CHAOS_TOKEN" //nolint:gosec // G101: the name of a variable, not a secret.
	dockerHostVariable    = "DOCKER_HOST"
	projectVariable       = "CENTAVO_COMPOSE_PROJECT"
	partnerURLVariable    = "PARTNER_ADMIN_URL"
	participantVariable   = "PARTNER_PARTICIPANT_ID"
	fileScheme            = "file"
	minChaosTokenLength   = 32
)

// Errors of reading the configuration.
var (
	ErrMissingVariable  = errors.New("required environment variable is not set")
	ErrInvalidBankURL   = errors.New("BANK_API_URL must be an absolute http or https URL")
	ErrUnsupportedStore = errors.New("ARTIFACT_STORE must be a file:// URL with an absolute path")
	ErrInvalidAuditor   = errors.New("AUDITOR_DATABASE_URL must be a postgres:// URL")
	ErrWeakChaosToken   = errors.New("CENTAVO_CHAOS_TOKEN must have at least 32 characters")
)

// LookupFunc reads one environment variable; os.LookupEnv satisfies it.
type LookupFunc func(key string) (string, bool)

// Serve is the configuration of `lab serve`.
type Serve struct {
	// RoundsDir is where rounds are archived.
	RoundsDir string
	// AuditorURL connects straight to PostgreSQL as bank_auditor, never
	// through Toxiproxy: auditing is not a chaos target.
	AuditorURL string
}

// Run is the configuration of `lab run`.
type Run struct {
	Serve
	// BankURL is the bank's client API, through the client_api proxy.
	BankURL string
	// ControlURL is the same API without Toxiproxy, for the cure's calls.
	ControlURL string
	// ChaosToken authenticates the lab on the chaos ports.
	ChaosToken string
	// DockerHost is docker-proxy, the only way to the Docker API.
	DockerHost string
	// Project is the Compose project whose chaos targets the bot may touch.
	Project string
	// PartnerURL is the partner's admin port: PIX in, truth and lies.
	PartnerURL string
	// ParticipantID is the bank at the partner.
	ParticipantID string
}

// LoadServe reads the configuration of `lab serve`.
func LoadServe(lookup LookupFunc) (Serve, error) {
	store, err := require(lookup, artifactStoreVariable)
	if err != nil {
		return Serve{}, err
	}

	roundsDir, err := roundsDirOf(store)
	if err != nil {
		return Serve{}, err
	}

	auditorURL, err := require(lookup, auditorURLVariable)
	if err != nil {
		return Serve{}, err
	}

	err = validateAuditorURL(auditorURL)
	if err != nil {
		return Serve{}, err
	}

	return Serve{RoundsDir: roundsDir, AuditorURL: auditorURL}, nil
}

// LoadRun reads the configuration of `lab run`.
func LoadRun(lookup LookupFunc) (Run, error) {
	serve, err := LoadServe(lookup)
	if err != nil {
		return Run{}, err
	}

	bankURL, err := require(lookup, bankURLVariable)
	if err != nil {
		return Run{}, err
	}

	err = validateBankURL(bankURL)
	if err != nil {
		return Run{}, err
	}

	controlURL, err := require(lookup, controlURLVariable)
	if err != nil {
		return Run{}, err
	}

	err = validateBankURL(controlURL)
	if err != nil {
		return Run{}, err
	}

	chaosToken, err := require(lookup, chaosTokenVariable)
	if err != nil {
		return Run{}, err
	}

	if len(chaosToken) < minChaosTokenLength {
		return Run{}, ErrWeakChaosToken
	}

	dockerHost, err := require(lookup, dockerHostVariable)
	if err != nil {
		return Run{}, err
	}

	project, err := require(lookup, projectVariable)
	if err != nil {
		return Run{}, err
	}

	partnerURL, err := require(lookup, partnerURLVariable)
	if err != nil {
		return Run{}, err
	}

	err = validateBankURL(partnerURL)
	if err != nil {
		return Run{}, err
	}

	participant, err := require(lookup, participantVariable)
	if err != nil {
		return Run{}, err
	}

	run := Run{
		Serve: serve, BankURL: bankURL, ControlURL: controlURL, ChaosToken: chaosToken, DockerHost: dockerHost,
		Project: project, PartnerURL: partnerURL, ParticipantID: participant,
	}

	return run, nil
}

// validateAuditorURL never echoes the URL back: it carries a password.
func validateAuditorURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ErrInvalidAuditor
	}

	isPostgres := parsed.Scheme == "postgres" || parsed.Scheme == "postgresql"
	if !isPostgres || parsed.Host == "" {
		return ErrInvalidAuditor
	}

	return nil
}

func validateBankURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: got %q", ErrInvalidBankURL, raw)
	}

	isHTTP := parsed.Scheme == "http" || parsed.Scheme == "https"
	if !isHTTP || parsed.Host == "" {
		return fmt.Errorf("%w: got %q", ErrInvalidBankURL, raw)
	}

	return nil
}

// roundsDirOf maps the store URL to a directory. S3 arrives with an earlier
// version.
func roundsDirOf(store string) (string, error) {
	parsed, err := url.Parse(store)
	if err != nil {
		return "", fmt.Errorf("%w: got %q", ErrUnsupportedStore, store)
	}

	isLocalPath := parsed.Scheme == fileScheme && parsed.Host == "" && filepath.IsAbs(parsed.Path)
	if !isLocalPath {
		return "", fmt.Errorf("%w: got %q", ErrUnsupportedStore, store)
	}

	return filepath.Clean(parsed.Path), nil
}

func require(lookup LookupFunc, key string) (string, error) {
	value, isSet := lookup(key)

	isMissing := !isSet || value == ""
	if isMissing {
		return "", fmt.Errorf("%w: %s", ErrMissingVariable, key)
	}

	return value, nil
}
