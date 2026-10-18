// Package scenarios embeds and parses the named scenarios of
// docs/chaos-bot.md, section 13. Only the fields the lab implements are
// accepted; an unknown field fails loudly instead of being ignored.
package scenarios

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
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

// The operation mixes of a workload.
const (
	ProfileLedgerOnly    Profile = "ledger-only"
	ProfileBalanced      Profile = "balanced"
	ProfilePaymentsHeavy Profile = "payments-heavy"
	// ProfilePaymentsOut has no PIX in: a scenario that cuts every webhook
	// leaves a PIX in for the reconciliation alone to find (F9).
	ProfilePaymentsOut Profile = "payments-out"
)

var workloadProfiles = []Profile{ProfileLedgerOnly, ProfileBalanced, ProfilePaymentsHeavy, ProfilePaymentsOut}

// ClientProfile is how a client behaves around timeouts and retries.
type ClientProfile string

// The client behaviors around timeouts and retries.
const (
	ClientPolite ClientProfile = "polite"
	// ClientDuplicator sends each operation twice, in parallel, with the same key.
	ClientDuplicator ClientProfile = "duplicator"
	// ClientAggressive retries at once, up to 20 times, with the same key.
	ClientAggressive ClientProfile = "aggressive"
	// ClientBuggy reuses the previous operation's key with another body.
	ClientBuggy ClientProfile = "buggy"
)

var clientProfiles = []ClientProfile{ClientPolite, ClientDuplicator, ClientAggressive, ClientBuggy}

// FaultKind is the fault family of docs/chaos-bot.md, section 4.
type FaultKind string

// The fault families.
const (
	FaultFailpoint FaultKind = "failpoint"
	FaultProcess   FaultKind = "process"
	// FaultPartner makes the partner lie (docs/chaos-bot.md, section 9).
	FaultPartner FaultKind = "partner"
	// FaultClock skews a process's wall clock (docs/chaos-bot.md, section 6).
	FaultClock FaultKind = "clock"
	// FaultNetwork puts toxics on Toxiproxy's links (docs/chaos-bot.md, section 8).
	FaultNetwork FaultKind = "network"
	// FaultDatabase kills or freezes PostgreSQL (docs/chaos-bot.md, section 10).
	FaultDatabase FaultKind = "database"
)

// Database actions (docs/chaos-bot.md, section 10).
const (
	DatabaseKillPostmaster = "kill_postmaster"
	DatabaseKillBackend    = "kill_backend"
	DatabaseFreeze         = "freeze"
	// DatabaseFailover kills the primary and promotes the replica (replica
	// profile only, docs/architecture.md, section 5.10).
	DatabaseFailover = "failover"
)

// Replication modes of the replica profile (docs/chaos-bot.md, section 10).
const (
	ReplicationAsync = "async"
	ReplicationSync  = "sync"
)

var (
	databaseActions  = []string{DatabaseKillPostmaster, DatabaseKillBackend, DatabaseFreeze, DatabaseFailover}
	replicationModes = []string{ReplicationAsync, ReplicationSync}
)

// Toxic types the bot puts on a link, with the three partitions of
// docs/chaos-bot.md, section 8: reject (the proxy is down), blackhole
// (nothing moves either way) and one_way (requests arrive, answers do not).
const (
	ToxicReject    = "reject"
	ToxicBlackhole = "blackhole"
	ToxicOneWay    = "one_way"
)

var (
	toxicTypes = []string{"latency", "timeout", "reset_peer", "limit_data", "slicer", "bandwidth", "slow_close", ToxicReject, ToxicBlackhole, ToxicOneWay}
	// networkLinks are Toxiproxy's proxies (docs/architecture.md, section 4.1).
	networkLinks = []string{"client_api", "api_pg", "worker1_pg", "worker2_pg", "worker1_partner", "worker2_partner", "partner_webhooks"}
	toxicStreams = []string{"upstream", "downstream"}
)

// partnerModeFields are the lies a scenario may ask for; the lab adds the
// participant, the seed and the TTL itself.
var partnerModeFields = []string{
	"timeout_after_process", "error_after_process", "double_process", "return_after_settle", "reject_rate",
	"settle_delay", "webhook_duplicate", "webhook_duplicate_new_id", "webhook_delay_max",
	"webhook_reorder_window", "webhook_drop", "outage",
}

// ScheduleType is how a scheduled fault spreads over the round.
type ScheduleType string

// The ways a scheduled fault spreads over the round.
const (
	SchedulePoisson ScheduleType = "poisson"
	// ScheduleAt fires once, at a fixed time after the round starts.
	ScheduleAt ScheduleType = "at"
)

// Targets the bot may hit; failpoints live only in the bank processes.
var (
	processTargets   = []string{"bank-api", "bank-worker-1", "bank-worker-2", "partner"}
	failpointTargets = []string{"bank-api", "bank-worker-1", "bank-worker-2"}
	failpointActions = []string{"kill", "delay", "error", "panic", "freeze"}
	failpointPattern = regexp.MustCompile(`^[a-z_]+\.[a-z_]+$`)
)

const (
	defaultHealTimeout = 2 * time.Minute
	// defaultLivenessDeadline is Δ of docs/invariants-and-checker.md, section 6.
	defaultLivenessDeadline = 120 * time.Second
	minMeanInterval         = time.Second
)

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

// Errors of loading a scenario.
var (
	ErrUnknownScenario = errors.New("unknown scenario")
	ErrInvalidScenario = errors.New("invalid scenario")
)

// Scenario is a validated scenario.
type Scenario struct {
	Name        string
	Description string
	Duration    time.Duration
	HealTimeout time.Duration
	// LivenessDeadline is Δ: how long after the cure the sagas have to end (I5).
	LivenessDeadline time.Duration
	Workload         Workload
	Faults           []Fault
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

// Fault is one fault of a scenario. Failpoint faults use Failpoint, Action,
// Arg, Probability and MaxFires; process faults use Schedule.
type Fault struct {
	ID          string
	Kind        FaultKind
	Targets     []string
	Failpoint   string
	Action      string
	Arg         string
	Probability float64
	MaxFires    int
	Schedule    Schedule
	// Modes are the partner's lies, as JSON for PUT /chaos/modes.
	Modes json.RawMessage
	// Offset is a clock fault's skew, ahead when positive.
	Offset time.Duration
	// Proxies, Toxic and Window describe a network fault: which links, what
	// happens on them and, with a schedule, how long each episode lasts.
	Proxies []string
	Toxic   Toxic
	Window  Window
	// Replication and Lag set up a failover: how the replica follows the
	// primary and how far behind its WAL stream runs.
	Replication string
	Lag         time.Duration
}

// Toxic is what a network fault does to a link.
type Toxic struct {
	Type   string
	Stream string
	// Toxicity is the share of connections hit.
	Toxicity   float64
	Attributes map[string]int64
}

// Window bounds an episode of a scheduled network fault.
type Window struct {
	Min time.Duration
	Max time.Duration
}

// Schedule spreads a fault over the round.
type Schedule struct {
	Type         ScheduleType
	MeanInterval time.Duration
	// At is when a ScheduleAt fault fires, from the start of the round.
	At time.Duration
}

type rawScenario struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Duration    string      `yaml:"duration"`
	HealTimeout string      `yaml:"heal_timeout"`
	Liveness    string      `yaml:"liveness_deadline"`
	Workload    rawWorkload `yaml:"workload"`
	Faults      []rawFault  `yaml:"faults"`
}

type rawFault struct {
	ID          string         `yaml:"id"`
	Kind        FaultKind      `yaml:"kind"`
	Targets     []string       `yaml:"targets"`
	Failpoint   string         `yaml:"failpoint"`
	Action      string         `yaml:"action"`
	Arg         string         `yaml:"arg"`
	Probability float64        `yaml:"probability"`
	MaxFires    int            `yaml:"max_fires"`
	Schedule    rawSchedule    `yaml:"schedule"`
	Modes       map[string]any `yaml:"modes"`
	Offset      string         `yaml:"offset"`
	Proxies     []string       `yaml:"proxies"`
	Toxic       map[string]any `yaml:"toxic"`
	Duration    rawWindow      `yaml:"duration"`
	Replication string         `yaml:"replication"`
	Lag         string         `yaml:"lag"`
}

type rawWindow struct {
	Min string `yaml:"min"`
	Max string `yaml:"max"`
}

type rawSchedule struct {
	Type         ScheduleType `yaml:"type"`
	MeanInterval string       `yaml:"mean_interval"`
	At           string       `yaml:"at"`
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

	healTimeout, err := optionalDuration(raw.HealTimeout, defaultHealTimeout)
	if err != nil {
		return Scenario{}, fmt.Errorf("heal_timeout: %w", err)
	}

	liveness, err := optionalDuration(raw.Liveness, defaultLivenessDeadline)
	if err != nil {
		return Scenario{}, fmt.Errorf("liveness_deadline: %w", err)
	}

	workload, err := validateWorkload(raw.Workload)
	if err != nil {
		return Scenario{}, err
	}

	faults, err := validateFaults(raw.Faults)
	if err != nil {
		return Scenario{}, err
	}

	scenario := Scenario{
		Name:             raw.Name,
		Description:      raw.Description,
		Duration:         duration,
		HealTimeout:      healTimeout,
		LivenessDeadline: liveness,
		Workload:         workload,
		Faults:           faults,
	}

	return scenario, nil
}

func optionalDuration(raw string, fallback time.Duration) (time.Duration, error) {
	if raw == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(raw)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("must be a positive duration (got %q)", raw)
	}

	return duration, nil
}

func validateFaults(raw []rawFault) ([]Fault, error) {
	faults := make([]Fault, 0, len(raw))
	seen := map[string]bool{}

	for _, r := range raw {
		if r.ID == "" || seen[r.ID] {
			return nil, fmt.Errorf("every fault needs a unique id (got %q)", r.ID)
		}

		seen[r.ID] = true

		fault, err := validateFault(r)
		if err != nil {
			return nil, fmt.Errorf("fault %s: %w", r.ID, err)
		}

		faults = append(faults, fault)
	}

	return faults, nil
}

func validateFault(r rawFault) (Fault, error) {
	fault := Fault{ID: r.ID, Kind: r.Kind, Targets: slices.Clone(r.Targets)}

	switch r.Kind {
	case FaultFailpoint:
		return validateFailpointFault(fault, r)
	case FaultProcess:
		return validateProcessFault(fault, r)
	case FaultPartner:
		return validatePartnerFault(fault, r)
	case FaultClock:
		return validateClockFault(fault, r)
	case FaultNetwork:
		return validateNetworkFault(fault, r)
	case FaultDatabase:
		return validateDatabaseFault(fault, r)
	default:
		return Fault{}, fmt.Errorf(
			"kind %q is not implemented (known: %s, %s, %s, %s, %s, %s)",
			r.Kind,
			FaultFailpoint,
			FaultProcess,
			FaultPartner,
			FaultClock,
			FaultNetwork,
			FaultDatabase,
		)
	}
}

// validatePartnerFault checks the names of the lies; the partner itself
// validates their values when the lab arms them.
func validatePartnerFault(fault Fault, r rawFault) (Fault, error) {
	if len(r.Modes) == 0 {
		return Fault{}, errors.New("a partner fault needs modes")
	}

	for _, name := range slices.Sorted(maps.Keys(r.Modes)) {
		if !slices.Contains(partnerModeFields, name) {
			return Fault{}, fmt.Errorf("partner mode %q is not one of %s", name, strings.Join(partnerModeFields, ", "))
		}
	}

	modes, err := json.Marshal(r.Modes)
	if err != nil {
		return Fault{}, fmt.Errorf("partner modes: %w", err)
	}

	fault.Modes = modes

	return fault, nil
}

func validateFailpointFault(fault Fault, r rawFault) (Fault, error) {
	err := validateTargets(r.Targets, failpointTargets)
	if err != nil {
		return Fault{}, err
	}

	isProbability := r.Probability > 0 && r.Probability <= 1
	isValid := failpointPattern.MatchString(r.Failpoint) && slices.Contains(failpointActions, r.Action) && isProbability && r.MaxFires >= 0
	if !isValid {
		return Fault{}, errors.New("a failpoint fault needs failpoint, an action (kill, delay, error, panic, freeze), probability in (0, 1] and max_fires >= 0")
	}

	fault.Failpoint, fault.Action, fault.Arg = r.Failpoint, r.Action, r.Arg
	fault.Probability, fault.MaxFires = r.Probability, r.MaxFires

	return fault, nil
}

// maxClockSkew bounds a clock fault, as the bank does.
const maxClockSkew = 48 * time.Hour

func validateClockFault(fault Fault, r rawFault) (Fault, error) {
	err := validateTargets(r.Targets, failpointTargets)
	if err != nil {
		return Fault{}, err
	}

	offset, err := time.ParseDuration(r.Offset)

	isValid := err == nil && offset != 0 && offset >= -maxClockSkew && offset <= maxClockSkew
	if !isValid {
		return Fault{}, fmt.Errorf("a clock fault needs a non-zero offset within %s, such as +120s", maxClockSkew)
	}

	fault.Offset = offset

	return fault, nil
}

func validateProcessFault(fault Fault, r rawFault) (Fault, error) {
	err := validateTargets(r.Targets, processTargets)
	if err != nil {
		return Fault{}, err
	}

	if r.Schedule.Type != SchedulePoisson {
		return Fault{}, fmt.Errorf("schedule type %q is not implemented (known: %s)", r.Schedule.Type, SchedulePoisson)
	}

	mean, err := time.ParseDuration(r.Schedule.MeanInterval)
	if err != nil || mean < minMeanInterval {
		return Fault{}, fmt.Errorf("mean_interval must be at least %s", minMeanInterval)
	}

	fault.Schedule = Schedule{Type: r.Schedule.Type, MeanInterval: mean}

	return fault, nil
}

func validateTargets(targets, allowed []string) error {
	if len(targets) == 0 {
		return errors.New("targets cannot be empty")
	}

	for _, target := range targets {
		if !slices.Contains(allowed, target) {
			return fmt.Errorf("target %q is not allowed here (allowed: %s)", target, strings.Join(allowed, ", "))
		}
	}

	return nil
}

func validateWorkload(raw rawWorkload) (Workload, error) {
	if !slices.Contains(workloadProfiles, raw.Profile) {
		return Workload{}, fmt.Errorf("workload profile %q is not implemented (known: ledger-only, balanced, payments-heavy)", raw.Profile)
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
		if !slices.Contains(clientProfiles, profile) {
			return fmt.Errorf("client profile %q is not implemented (known: polite, duplicator, aggressive, buggy)", profile)
		}

		if mix[profile] <= 0 {
			return fmt.Errorf("client profile %q needs a positive weight", profile)
		}
	}

	return nil
}
