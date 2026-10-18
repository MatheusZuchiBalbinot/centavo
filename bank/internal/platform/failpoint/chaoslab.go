//go:build chaoslab

package failpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	mathrand "math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"centavo/bank/internal/platform/correlation"
)

// Action is what a failpoint does when it fires.
type Action string

// The actions a failpoint can take.
const (
	ActionKill  Action = "kill"
	ActionDelay Action = "delay"
	ActionError Action = "error"
	ActionPanic Action = "panic"
	// ActionFreeze blocks this path and asks the lab to pause the whole
	// container for the same time.
	ActionFreeze Action = "freeze"
)

const (
	// maxTTL bounds every rule: chaos stops by itself if the lab dies.
	maxTTL = time.Hour
	// envRuleTTL is the life of a rule armed by CENTAVO_FAILPOINTS.
	envRuleTTL = time.Hour
	maxDelay   = 30 * time.Second
	// maxFreeze lets a pause outlast the 30 s leases with room to spare.
	maxFreeze = 2 * time.Minute
	specSep   = ","
)

// known lists the failpoints compiled into the bank; arming any other name fails.
var known = []Name{
	TransferAfterDebitBeforeCredit,
	APIAfterCommitBeforeResponse,
	RelayAfterPartnerCallBeforeSave,
	PixOutAfterSaveBeforePublish,
	WebhookAfterReceiveBeforeAck,
	InboxAfterApplyBeforeMark,
	ReconAfterComputeBeforeApply,
	ReconMidApply,
	LeaseAfterCheckBeforeWrite,
	SweeperAfterStatusBeforeTransition,
}

// specPattern is name=[probability%]action[(argument)][*max_fires].
var specPattern = regexp.MustCompile(`^([a-z_.]+)=(?:([0-9]+(?:\.[0-9]+)?)%)?([a-z]+)(?:\(([^)]*)\))?(?:\*([0-9]+))?$`)

// Errors of parsing failpoint rules.
var (
	ErrUnknownFailpoint = errors.New("unknown failpoint")
	ErrInvalidRule      = errors.New("invalid failpoint rule")
	ErrSynthetic        = errors.New("synthetic failure injected by a failpoint")
	errEventsDisabled   = errors.New("failpoint events directory is not configured")
)

// Match narrows a rule to some requests, for exact reproduction.
type Match struct {
	CorrelationPrefix string `json:"correlation_prefix,omitempty"`
	CorrelationID     string `json:"correlation_id,omitempty"`
}

// Rule arms one failpoint.
type Rule struct {
	Action      Action  `json:"action"`
	Arg         string  `json:"arg,omitempty"`
	Probability float64 `json:"probability"`
	// MaxFires is 0 for no limit.
	MaxFires int    `json:"max_fires,omitempty"`
	SkipHits int    `json:"skip_hits,omitempty"`
	Match    Match  `json:"match"`
	TTL      string `json:"ttl"`
	// Seed makes the decisions of this rule reproducible.
	Seed uint64 `json:"seed"`
}

// RuleState is a rule with its counters, as GET /chaos/failpoints shows it.
type RuleState struct {
	Name      Name      `json:"name"`
	Rule      Rule      `json:"rule"`
	Hits      int       `json:"hits"`
	Fires     int       `json:"fires"`
	ExpiresAt time.Time `json:"expires_at"`
}

// EventsConfig says where fired failpoints are recorded.
type EventsConfig struct {
	Dir      string
	Service  string
	Instance string
}

type armed struct {
	rule      Rule
	delay     time.Duration
	expiresAt time.Time
	hits      int
	fires     int
	decisions *mathrand.Rand
}

type firing struct {
	name  Name
	rule  Rule
	delay time.Duration
	hit   int
	fire  int
}

var registry = struct {
	mu    sync.Mutex
	rules map[Name]*armed
}{rules: map[Name]*armed{}}

// Arm installs or renews a rule. Re-arming the same rule only extends its
// life, so the lab can renew it like a lease without resetting the counters.
func Arm(name Name, rule Rule, now time.Time) error {
	ttl, delay, err := validate(name, rule)
	if err != nil {
		return err
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()

	existing, isArmed := registry.rules[name]
	if isArmed && existing.rule == rule {
		existing.expiresAt = now.Add(ttl)

		return nil
	}

	registry.rules[name] = &armed{rule: rule, delay: delay, expiresAt: now.Add(ttl), decisions: newDecisions(rule.Seed, name)}

	return nil
}

// DisarmAll clears every rule: the cure.
func DisarmAll() {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	clear(registry.rules)
}

// Rules lists the live rules and their counters.
func Rules(now time.Time) []RuleState {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	states := make([]RuleState, 0, len(registry.rules))

	for name, a := range registry.rules {
		if now.After(a.expiresAt) {
			continue
		}

		states = append(states, RuleState{Name: name, Rule: a.rule, Hits: a.hits, Fires: a.fires, ExpiresAt: a.expiresAt})
	}

	slices.SortFunc(states, func(a, b RuleState) int { return strings.Compare(string(a.Name), string(b.Name)) })

	return states
}

// ArmFromSpec arms the rules of CENTAVO_FAILPOINTS, to reproduce a case
// without the lab: "relay.after_partner_call_before_save=5%kill*50,...".
func ArmFromSpec(spec string, seed uint64, now time.Time) error {
	if strings.TrimSpace(spec) == "" {
		return nil
	}

	for part := range strings.SplitSeq(spec, specSep) {
		name, rule, err := parseSpec(strings.TrimSpace(part), seed)
		if err != nil {
			return err
		}

		err = Arm(name, rule, now)
		if err != nil {
			return err
		}
	}

	return nil
}

var events = struct {
	mu     sync.Mutex
	config EventsConfig
	file   *os.File
}{}

// ConfigureEvents sets where fired failpoints are recorded. Until it is
// called, no failpoint fires: an unrecorded fault would be invisible chaos.
func ConfigureEvents(config EventsConfig) {
	events.mu.Lock()
	defer events.mu.Unlock()

	events.config = config
}

func hit(ctx context.Context, name Name) error {
	f, shouldFire := decide(ctx, name, time.Now())
	if !shouldFire {
		return nil
	}

	err := record(ctx, f)
	if err != nil {
		// Never act without a record: the history would show a fault nobody injected.
		return nil //nolint:nilerr // dropping the fault is the answer to a failed record
	}

	return execute(ctx, f)
}

func decide(ctx context.Context, name Name, now time.Time) (firing, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()

	a, isArmed := registry.rules[name]
	if !isArmed {
		return firing{}, false
	}

	if now.After(a.expiresAt) {
		delete(registry.rules, name)

		return firing{}, false
	}

	if !matches(a.rule.Match, correlation.From(ctx)) {
		return firing{}, false
	}

	a.hits++

	isSkipped := a.hits <= a.rule.SkipHits
	isExhausted := a.rule.MaxFires > 0 && a.fires >= a.rule.MaxFires
	if isSkipped || isExhausted {
		return firing{}, false
	}

	// One draw per eligible hit: the same seed and the same hits give the same
	// decisions.
	if a.decisions.Float64() >= a.rule.Probability {
		return firing{}, false
	}

	a.fires++

	return firing{name: name, rule: a.rule, delay: a.delay, hit: a.hits, fire: a.fires}, true
}

func matches(match Match, correlationID string) bool {
	if match.CorrelationID != "" && match.CorrelationID != correlationID {
		return false
	}

	return strings.HasPrefix(correlationID, match.CorrelationPrefix)
}

func execute(ctx context.Context, f firing) error {
	switch f.rule.Action {
	case ActionKill:
		// The service is not PID 1 (init: true), so SIGKILL to itself works.
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {}
	case ActionDelay, ActionFreeze:
		timer := time.NewTimer(f.delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	case ActionError:
		return fmt.Errorf("%w: %s at %s", ErrSynthetic, f.rule.Arg, f.name)
	case ActionPanic:
		panic(fmt.Sprintf("failpoint %s fired with action panic", f.name))
	default:
		return nil
	}
}

type event struct {
	TS            time.Time `json:"ts"`
	Service       string    `json:"service"`
	Instance      string    `json:"instance"`
	PID           int       `json:"pid"`
	Failpoint     Name      `json:"failpoint"`
	Action        Action    `json:"action"`
	Arg           string    `json:"arg,omitempty"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	Hit           int       `json:"hit"`
	Fire          int       `json:"fire"`
}

// record appends the event and fsyncs it before the action runs, so even a
// SIGKILL the process sends itself is in the history.
func record(ctx context.Context, f firing) error {
	events.mu.Lock()
	defer events.mu.Unlock()

	if events.config.Dir == "" {
		return errEventsDisabled
	}

	if events.file == nil {
		path := filepath.Join(events.config.Dir, events.config.Instance+".jsonl")

		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o640) //nolint:gosec // G304: the path comes from the process configuration.
		if err != nil {
			return err
		}

		events.file = file
	}

	line, err := json.Marshal(event{
		TS: time.Now().UTC(), Service: events.config.Service, Instance: events.config.Instance, PID: os.Getpid(),
		Failpoint: f.name, Action: f.rule.Action, Arg: f.rule.Arg, CorrelationID: correlation.From(ctx), Hit: f.hit, Fire: f.fire,
	})
	if err != nil {
		return err
	}

	_, err = events.file.Write(append(line, '\n'))
	if err != nil {
		return err
	}

	return events.file.Sync()
}

func validate(name Name, rule Rule) (time.Duration, time.Duration, error) {
	if !slices.Contains(known, name) {
		return 0, 0, fmt.Errorf("%w: %q", ErrUnknownFailpoint, name)
	}

	ttl, err := time.ParseDuration(rule.TTL)
	if err != nil || ttl <= 0 || ttl > maxTTL {
		return 0, 0, fmt.Errorf("%w: ttl must be between 0 and %s", ErrInvalidRule, maxTTL)
	}

	isValidCounters := rule.Probability >= 0 && rule.Probability <= 1 && rule.MaxFires >= 0 && rule.SkipHits >= 0
	if !isValidCounters {
		return 0, 0, fmt.Errorf("%w: probability in [0, 1], max_fires and skip_hits not negative", ErrInvalidRule)
	}

	delay, err := validateAction(rule)

	return ttl, delay, err
}

func validateAction(rule Rule) (time.Duration, error) {
	switch rule.Action {
	case ActionKill, ActionPanic:
		return 0, nil
	case ActionDelay:
		delay, err := time.ParseDuration(rule.Arg)
		if err != nil || delay <= 0 || delay > maxDelay {
			return 0, fmt.Errorf("%w: delay needs a duration up to %s", ErrInvalidRule, maxDelay)
		}

		return delay, nil
	case ActionError:
		if rule.Arg == "" {
			return 0, fmt.Errorf("%w: error needs a code, such as error(conn_reset)", ErrInvalidRule)
		}

		return 0, nil
	case ActionFreeze:
		freeze, err := time.ParseDuration(rule.Arg)
		if err != nil || freeze <= 0 || freeze > maxFreeze {
			return 0, fmt.Errorf("%w: freeze needs a duration up to %s", ErrInvalidRule, maxFreeze)
		}

		return freeze, nil
	default:
		return 0, fmt.Errorf("%w: unknown action %q", ErrInvalidRule, rule.Action)
	}
}

func parseSpec(part string, seed uint64) (Name, Rule, error) {
	match := specPattern.FindStringSubmatch(part)
	if match == nil {
		return "", Rule{}, fmt.Errorf("%w: %q does not read name=[p%%]action[(arg)][*max]", ErrInvalidRule, part)
	}

	rule := Rule{Action: Action(match[3]), Arg: match[4], Probability: 1, TTL: envRuleTTL.String(), Seed: seed}

	if match[2] != "" {
		percent, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			return "", Rule{}, fmt.Errorf("%w: probability %q", ErrInvalidRule, match[2])
		}

		rule.Probability = percent / 100
	}

	if match[5] != "" {
		maxFires, err := strconv.Atoi(match[5])
		if err != nil {
			return "", Rule{}, fmt.Errorf("%w: max fires %q", ErrInvalidRule, match[5])
		}

		rule.MaxFires = maxFires
	}

	return Name(match[1]), rule, nil
}

func newDecisions(seed uint64, name Name) *mathrand.Rand {
	hasher := fnv.New64a()

	// Writing to an in-memory hash never fails.
	_, _ = hasher.Write([]byte(name))

	// Determinism is the point: the same seed must replay the same decisions.
	return mathrand.New(mathrand.NewPCG(seed, hasher.Sum64())) //nolint:gosec // G404: reproducibility, not secrecy.
}
