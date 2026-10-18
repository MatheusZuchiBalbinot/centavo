// Package modes holds the partner's lies: probabilities per participant, with
// their own seed and a TTL. Without an active mode the partner is honest. Only
// the chaoslab build can set a mode.
package modes

import (
	"encoding/hex"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"reflect"
	"strings"
	"sync"
	"time"

	"centavo/partner/internal/truth"
)

// HonestSettleDelay is how long an honest partner takes to settle.
const HonestSettleDelay = 300 * time.Millisecond

const (
	maxTTL         = time.Hour
	maxDelay       = 10 * time.Minute
	returnDelayMin = time.Second
	returnDelayMax = 5 * time.Second
)

// OutageMode is how the partner is unavailable.
type OutageMode string

// The ways the partner can be down.
const (
	OutageServiceUnavailable OutageMode = "503"
	OutageRefuse             OutageMode = "refuse"
)

// ErrInvalidMode means a mode request does not decode or is out of range.
var ErrInvalidMode = errors.New("invalid partner mode")

// Range is a duration range.
type Range struct {
	Min string `json:"min"`
	Max string `json:"max"`
}

// OutageConfig is a window during which the partner is down.
type OutageConfig struct {
	// Until is relative to the moment the mode is set, such as "+3m".
	Until string     `json:"until"`
	Mode  OutageMode `json:"mode"`
}

// Config is the body of PUT /chaos/modes.
type Config struct {
	ParticipantID         string        `json:"participant_id"`
	Seed                  string        `json:"seed"`
	TTL                   string        `json:"ttl"`
	TimeoutAfterProcess   float64       `json:"timeout_after_process"`
	ErrorAfterProcess     float64       `json:"error_after_process"`
	DoubleProcess         float64       `json:"double_process"`
	ReturnAfterSettle     float64       `json:"return_after_settle"`
	RejectRate            float64       `json:"reject_rate"`
	SettleDelay           *Range        `json:"settle_delay"`
	WebhookDuplicate      float64       `json:"webhook_duplicate"`
	WebhookDuplicateNewID float64       `json:"webhook_duplicate_new_id"`
	WebhookDelayMax       string        `json:"webhook_delay_max"`
	WebhookReorderWindow  string        `json:"webhook_reorder_window"`
	WebhookDrop           float64       `json:"webhook_drop"`
	Outage                *OutageConfig `json:"outage"`
}

// mode is a validated, active configuration.
type mode struct {
	config        Config
	expiresAt     time.Time
	settleMin     time.Duration
	settleMax     time.Duration
	delayMax      time.Duration
	reorderWindow time.Duration
	outageUntil   time.Time
	hasOutage     bool
	draws         *mathrand.Rand
	nextEventCopy int
}

// Registry keeps the active mode of each participant.
type Registry struct {
	mu     sync.Mutex
	active map[string]*mode
}

// NewRegistry starts honest.
func NewRegistry() *Registry {
	return &Registry{active: map[string]*mode{}}
}

// Set validates and activates a mode, replacing the participant's previous
// one. Setting the same mode again only extends its life: the lab renews it
// like a lease, without restarting the outage window or the sequence of lies.
func (r *Registry) Set(config Config, now time.Time) error {
	m, err := newMode(config, now)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	existing, isActive := r.active[config.ParticipantID]

	isRenewal := isActive && reflect.DeepEqual(existing.config, config) && now.Before(existing.expiresAt)
	if isRenewal {
		existing.expiresAt = m.expiresAt

		return nil
	}

	r.active[config.ParticipantID] = m

	return nil
}

// Clear makes the partner honest again; deliveries already planned still go out.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	clear(r.active)
}

// Decider answers the partner's questions for one participant at one moment.
type Decider struct {
	registry    *Registry
	participant string
	now         time.Time
}

// For returns the decider of a participant.
func (r *Registry) For(participant string, now time.Time) Decider {
	return Decider{registry: r, participant: participant, now: now}
}

// draw runs fn on the active mode under the lock, or returns the honest answer.
func draw[T any](d Decider, honest T, fn func(m *mode) T) T {
	d.registry.mu.Lock()
	defer d.registry.mu.Unlock()

	m, isActive := d.registry.active[d.participant]
	if !isActive {
		return honest
	}

	if d.now.After(m.expiresAt) {
		delete(d.registry.active, d.participant)

		return honest
	}

	return fn(m)
}

// Outage reports whether the partner is down for this participant now.
func (d Decider) Outage() (OutageMode, bool) {
	type answer struct {
		mode   OutageMode
		isDown bool
	}

	result := draw(d, answer{}, func(m *mode) answer {
		isDown := m.hasOutage && d.now.Before(m.outageUntil)
		if !isDown {
			return answer{}
		}

		return answer{mode: m.config.Outage.Mode, isDown: true}
	})

	return result.mode, result.isDown
}

// TimeoutAfterProcess reports whether to process the transfer, then answer too
// late.
func (d Decider) TimeoutAfterProcess() bool {
	return draw(d, false, func(m *mode) bool { return m.draws.Float64() < m.config.TimeoutAfterProcess })
}

// ErrorAfterProcess reports whether to process the transfer, then answer 500.
func (d Decider) ErrorAfterProcess() bool {
	return draw(d, false, func(m *mode) bool { return m.draws.Float64() < m.config.ErrorAfterProcess })
}

// DoubleProcess reports whether to settle the transfer twice, the partner's own
// bug.
func (d Decider) DoubleProcess() bool {
	return draw(d, false, func(m *mode) bool { return m.draws.Float64() < m.config.DoubleProcess })
}

// Reject reports whether to refuse a transfer that was accepted.
func (d Decider) Reject() bool {
	return draw(d, false, func(m *mode) bool { return m.draws.Float64() < m.config.RejectRate })
}

// ReturnAfterSettle returns when to give a settled transfer back, if at all.
func (d Decider) ReturnAfterSettle() *time.Time {
	return draw(d, (*time.Time)(nil), func(m *mode) *time.Time {
		if m.draws.Float64() >= m.config.ReturnAfterSettle {
			return nil
		}

		at := d.now.Add(between(m.draws, returnDelayMin, returnDelayMax))

		return &at
	})
}

// SettleDelay is how long until the transfer settles or is rejected.
func (d Decider) SettleDelay() time.Duration {
	return draw(d, HonestSettleDelay, func(m *mode) time.Duration {
		if m.settleMax == 0 {
			return HonestSettleDelay
		}

		return between(m.draws, m.settleMin, m.settleMax)
	})
}

// Plan decides how an event is delivered.
func (d Decider) Plan(event truth.Event, now time.Time) []truth.PlannedDelivery {
	honest := []truth.PlannedDelivery{{EventID: event.ID, At: now}}

	return draw(d, honest, func(m *mode) []truth.PlannedDelivery { return m.plan(event, now) })
}

func (m *mode) plan(event truth.Event, now time.Time) []truth.PlannedDelivery {
	first := truth.PlannedDelivery{EventID: event.ID, At: now.Add(m.lateness())}

	if m.draws.Float64() < m.config.WebhookDrop {
		first.IsDropped = true
	}

	planned := []truth.PlannedDelivery{first}

	if m.draws.Float64() < m.config.WebhookDuplicate {
		planned = append(planned, truth.PlannedDelivery{EventID: event.ID, At: now.Add(m.lateness())})
	}

	if m.draws.Float64() < m.config.WebhookDuplicateNewID {
		m.nextEventCopy++
		planned = append(planned, truth.PlannedDelivery{EventID: fmt.Sprintf("%s-copy%d", event.ID, m.nextEventCopy), At: now.Add(m.lateness())})
	}

	return planned
}

// lateness is the delay of one delivery: up to webhook_delay_max, and
// spread over the reorder window so events of one transfer can swap places.
func (m *mode) lateness() time.Duration {
	return between(m.draws, 0, m.delayMax) + between(m.draws, 0, m.reorderWindow)
}

func between(draws *mathrand.Rand, low, high time.Duration) time.Duration {
	if high <= low {
		return low
	}

	return low + time.Duration(draws.Int64N(int64(high-low)+1))
}

func newMode(config Config, now time.Time) (*mode, error) {
	if config.ParticipantID == "" {
		return nil, fmt.Errorf("%w: participant_id is required", ErrInvalidMode)
	}

	ttl, err := parseDuration(config.TTL, maxTTL)
	if err != nil || ttl == 0 {
		return nil, fmt.Errorf("%w: ttl must be between 0 and %s", ErrInvalidMode, maxTTL)
	}

	err = validateProbabilities(config)
	if err != nil {
		return nil, err
	}

	seed, err := parseSeed(config.Seed)
	if err != nil {
		return nil, err
	}

	draws := mathrand.New(mathrand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // G404: lies must replay from the seed.
	m := &mode{config: config, expiresAt: now.Add(ttl), draws: draws}

	err = m.parseDurations(now)
	if err != nil {
		return nil, err
	}

	return m, nil
}

func (m *mode) parseDurations(now time.Time) error {
	var err error

	if m.config.SettleDelay != nil {
		m.settleMin, err = parseDuration(m.config.SettleDelay.Min, maxDelay)
		if err == nil {
			m.settleMax, err = parseDuration(m.config.SettleDelay.Max, maxDelay)
		}
	}

	if err == nil {
		m.delayMax, err = parseDuration(m.config.WebhookDelayMax, maxDelay)
	}

	if err == nil {
		m.reorderWindow, err = parseDuration(m.config.WebhookReorderWindow, maxDelay)
	}

	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidMode, err)
	}

	return m.parseOutage(now)
}

func (m *mode) parseOutage(now time.Time) error {
	if m.config.Outage == nil {
		return nil
	}

	isKnownMode := m.config.Outage.Mode == OutageServiceUnavailable || m.config.Outage.Mode == OutageRefuse
	window, err := parseDuration(strings.TrimPrefix(m.config.Outage.Until, "+"), maxTTL)

	if err != nil || !isKnownMode {
		return fmt.Errorf("%w: outage needs until (+3m) and mode 503 or refuse", ErrInvalidMode)
	}

	m.hasOutage = true
	m.outageUntil = now.Add(window)

	return nil
}

func validateProbabilities(config Config) error {
	probabilities := []float64{
		config.TimeoutAfterProcess, config.ErrorAfterProcess, config.DoubleProcess, config.ReturnAfterSettle,
		config.RejectRate, config.WebhookDuplicate, config.WebhookDuplicateNewID, config.WebhookDrop,
	}

	for _, probability := range probabilities {
		if probability < 0 || probability > 1 {
			return fmt.Errorf("%w: probabilities must be between 0 and 1", ErrInvalidMode)
		}
	}

	return nil
}

func parseDuration(raw string, ceiling time.Duration) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}

	duration, err := time.ParseDuration(raw)
	if err != nil || duration < 0 || duration > ceiling {
		return 0, fmt.Errorf("duration %q must be between 0 and %s", raw, ceiling)
	}

	return duration, nil
}

func parseSeed(raw string) (uint64, error) {
	digits := strings.TrimPrefix(strings.ToLower(raw), "0x")
	if digits == "" {
		return 0, nil
	}

	if len(digits)%2 == 1 {
		digits = "0" + digits
	}

	decoded, err := hex.DecodeString(digits)
	if err != nil || len(decoded) > 8 {
		return 0, fmt.Errorf("%w: seed must be a hexadecimal number of up to 64 bits", ErrInvalidMode)
	}

	var seed uint64
	for _, b := range decoded {
		seed = seed<<8 | uint64(b)
	}

	return seed, nil
}
