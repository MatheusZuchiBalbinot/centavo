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
	artifactStoreVariable = "ARTIFACT_STORE"
	auditorURLVariable    = "AUDITOR_DATABASE_URL"
	fileScheme            = "file"
)

var (
	ErrMissingVariable  = errors.New("required environment variable is not set")
	ErrInvalidBankURL   = errors.New("BANK_API_URL must be an absolute http or https URL")
	ErrUnsupportedStore = errors.New("ARTIFACT_STORE must be a file:// URL with an absolute path")
	ErrInvalidAuditor   = errors.New("AUDITOR_DATABASE_URL must be a postgres:// URL")
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

	return Run{Serve: serve, BankURL: bankURL}, nil
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

// roundsDirOf maps the store URL to a directory. S3 arrives with phase F13.
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
