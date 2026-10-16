// Package scenarios embeds and parses the named scenarios of
// docs/chaos-bot.md, section 13. Only the fields the lab implements are
// accepted; an unknown field fails loudly instead of being ignored.
package scenarios

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

//go:embed *.yaml
var files embed.FS

// Profile is the operation mix of a workload (docs/chaos-bot.md, section 11).
type Profile string

const ProfileLedgerOnly Profile = "ledger-only"

// ClientProfile is how a client behaves around timeouts and retries.
type ClientProfile string

const ClientPolite ClientProfile = "polite"

const (
	maxClients       = 64
	maxRatePerSecond = 1_000
	minAccounts      = 2
	// minAccountsPerClient lets a client with exclusive accounts transfer
	// between two of its own.
	minAccountsPerClient = 2
	fileExtension        = ".yaml"
)

var namePattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var (
	ErrUnknownScenario = errors.New("unknown scenario")
	ErrInvalidScenario = errors.New("invalid scenario")
)

// Scenario is a validated scenario.
type Scenario struct {
	Name        string
	Description string
	Duration    time.Duration
	Workload    Workload
	// SHA256 identifies the exact YAML a round ran, for round.json.
	SHA256 string
}

// Workload describes the load generator of a round.
type Workload struct {
	Profile           Profile
	Clients           int
	RatePerSecond     float64
	Accounts          int
	ExclusiveAccounts bool
	ClientMix         map[ClientProfile]float64
}

type rawScenario struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Duration    string      `yaml:"duration"`
	Workload    rawWorkload `yaml:"workload"`
}

type rawWorkload struct {
	Profile           Profile                   `yaml:"profile"`
	Clients           int                       `yaml:"clients"`
	RatePerSecond     float64                   `yaml:"rate_per_second"`
	Accounts          int                       `yaml:"accounts"`
	ExclusiveAccounts bool                      `yaml:"exclusive_accounts"`
	ClientMix         map[ClientProfile]float64 `yaml:"client_mix"`
}

// Names lists the embedded scenarios.
func Names() []string {
	entries, err := fs.Glob(files, "*"+fileExtension)
	if err != nil {
		// The pattern is a constant; Glob only fails on a malformed pattern.
		panic(err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, strings.TrimSuffix(entry, fileExtension))
	}

	return names
}

// Load reads and validates one embedded scenario.
func Load(name string) (Scenario, error) {
	if !namePattern.MatchString(name) {
		return Scenario{}, fmt.Errorf("%w: %q", ErrUnknownScenario, name)
	}

	content, err := files.ReadFile(name + fileExtension)
	if err != nil {
		return Scenario{}, fmt.Errorf("%w: %q (known: %s)", ErrUnknownScenario, name, strings.Join(Names(), ", "))
	}

	return Parse(name, content)
}

// Parse validates a scenario file; name is the file name it must declare.
func Parse(name string, content []byte) (Scenario, error) {
	var raw rawScenario

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)

	err := decoder.Decode(&raw)
	if err != nil {
		return Scenario{}, fmt.Errorf("%w: %s: %w", ErrInvalidScenario, name, err)
	}

	scenario, err := validate(name, raw)
	if err != nil {
		return Scenario{}, fmt.Errorf("%w: %s: %w", ErrInvalidScenario, name, err)
	}

	digest := sha256.Sum256(content)
	scenario.SHA256 = hex.EncodeToString(digest[:])

	return scenario, nil
}

func validate(name string, raw rawScenario) (Scenario, error) {
	if raw.Name != name {
		return Scenario{}, fmt.Errorf("declares name %q", raw.Name)
	}

	duration, err := time.ParseDuration(raw.Duration)
	if err != nil || duration <= 0 {
		return Scenario{}, fmt.Errorf("duration must be positive, such as 5m (got %q)", raw.Duration)
	}

	workload, err := validateWorkload(raw.Workload)
	if err != nil {
		return Scenario{}, err
	}

	return Scenario{Name: raw.Name, Description: raw.Description, Duration: duration, Workload: workload}, nil
}

func validateWorkload(raw rawWorkload) (Workload, error) {
	if raw.Profile != ProfileLedgerOnly {
		return Workload{}, fmt.Errorf("workload profile %q is not implemented (known: %s)", raw.Profile, ProfileLedgerOnly)
	}

	hasValidClients := raw.Clients >= 1 && raw.Clients <= maxClients
	if !hasValidClients {
		return Workload{}, fmt.Errorf("clients must be between 1 and %d", maxClients)
	}

	hasValidRate := raw.RatePerSecond > 0 && raw.RatePerSecond <= maxRatePerSecond
	if !hasValidRate {
		return Workload{}, fmt.Errorf("rate_per_second must be above 0 and at most %d", maxRatePerSecond)
	}

	minimum := minAccounts
	if raw.ExclusiveAccounts {
		minimum = minAccountsPerClient * raw.Clients
	}

	if raw.Accounts < minimum {
		return Workload{}, fmt.Errorf("accounts must be at least %d for %d clients", minimum, raw.Clients)
	}

	err := validateClientMix(raw.ClientMix)
	if err != nil {
		return Workload{}, err
	}

	workload := Workload{
		Profile:           raw.Profile,
		Clients:           raw.Clients,
		RatePerSecond:     raw.RatePerSecond,
		Accounts:          raw.Accounts,
		ExclusiveAccounts: raw.ExclusiveAccounts,
		ClientMix:         maps.Clone(raw.ClientMix),
	}

	return workload, nil
}

func validateClientMix(mix map[ClientProfile]float64) error {
	if len(mix) == 0 {
		return errors.New("client_mix needs at least one client profile")
	}

	for _, profile := range slices.Sorted(maps.Keys(mix)) {
		if profile != ClientPolite {
			return fmt.Errorf("client profile %q is not implemented (known: %s)", profile, ClientPolite)
		}

		if mix[profile] <= 0 {
			return fmt.Errorf("client profile %q needs a positive weight", profile)
		}
	}

	return nil
}
