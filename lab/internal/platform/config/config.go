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
	fileScheme            = "file"
)

var (
	ErrMissingVariable  = errors.New("required environment variable is not set")
	ErrInvalidBankURL   = errors.New("BANK_API_URL must be an absolute http or https URL")
	ErrUnsupportedStore = errors.New("ARTIFACT_STORE must be a file:// URL with an absolute path")
)

// LookupFunc reads one environment variable; os.LookupEnv satisfies it.
type LookupFunc func(key string) (string, bool)

// Run is the configuration of `lab run`.
type Run struct {
	// BankURL is the bank's client API, through the client_api proxy.
	BankURL string
	// RoundsDir is where rounds are archived.
	RoundsDir string
}

// LoadRun reads the configuration of `lab run`.
func LoadRun(lookup LookupFunc) (Run, error) {
	bankURL, err := require(lookup, bankURLVariable)
	if err != nil {
		return Run{}, err
	}

	err = validateBankURL(bankURL)
	if err != nil {
		return Run{}, err
	}

	store, err := require(lookup, artifactStoreVariable)
	if err != nil {
		return Run{}, err
	}

	roundsDir, err := roundsDirOf(store)
	if err != nil {
		return Run{}, err
	}

	return Run{BankURL: bankURL, RoundsDir: roundsDir}, nil
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
