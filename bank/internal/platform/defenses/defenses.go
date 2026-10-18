// Package defenses parses the defense profile that selects, at run time,
// which defenses (D1 to D12) are active. See docs/architecture.md, section 8.
package defenses

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Defense is the flag name of one defense technique.
type Defense string

// The defenses, D1 to D12 (docs/defenses.md).
const (
	AtomicLedger       Defense = "atomic_ledger"
	BalanceLocking     Defense = "balance_locking"
	Idempotency        Defense = "idempotency"
	Outbox             Defense = "outbox"
	Inbox              Defense = "inbox"
	PartnerIdempotency Defense = "partner_idempotency"
	StateMachine       Defense = "state_machine"
	Recovery           Defense = "recovery"
	ResilientClient    Defense = "resilient_client"
	Reconciliation     Defense = "reconciliation"
	Fencing            Defense = "fencing"
	DurableDB          Defense = "durable_db"
)

const (
	robustKeyword    = "robust"
	naiveKeyword     = "naive"
	profileSeparator = ","
	disablePrefix    = "-"
)

// Errors of parsing a defense profile.
var (
	ErrEmptyProfile      = errors.New("defense profile is empty")
	ErrUnknownBase       = errors.New("defense profile must start with robust or naive")
	ErrNaiveWithModifier = errors.New("the naive profile takes no modifiers")
	ErrMissingPrefix     = errors.New("defense modifiers must start with '-'")
	ErrUnknownDefense    = errors.New("unknown defense")
	ErrDuplicateDefense  = errors.New("defense disabled more than once")
	// ErrNoNaiveVariant means the binary has no naive variant of a defense:
	// the production build never does (lock 1 of docs/chaos-bot.md, section 16).
	ErrNoNaiveVariant = errors.New("this binary has no naive variant of the defense")
)

// all lists the defenses in catalog order (D1 to D12); String relies on it
// to print a canonical profile.
var all = []Defense{
	AtomicLedger,
	BalanceLocking,
	Idempotency,
	Outbox,
	Inbox,
	PartnerIdempotency,
	StateMachine,
	Recovery,
	ResilientClient,
	Reconciliation,
	Fencing,
	DurableDB,
}

// Profile is an immutable set of disabled defenses.
type Profile struct {
	disabled []Defense
}

// All returns every defense in catalog order.
func All() []Defense {
	return slices.Clone(all)
}

// Robust returns the profile with every defense enabled.
func Robust() Profile {
	return Profile{}
}

// ParseProfile parses "robust", "naive" or "robust,-<defense>[,-<defense>...]".
func ParseProfile(raw string) (Profile, error) {
	tokens := strings.Split(raw, profileSeparator)
	base := strings.TrimSpace(tokens[0])
	modifiers := tokens[1:]

	if base == "" {
		return Profile{}, ErrEmptyProfile
	}

	if base == naiveKeyword {
		return parseNaive(modifiers)
	}

	if base != robustKeyword {
		return Profile{}, fmt.Errorf("%w: %q", ErrUnknownBase, base)
	}

	return parseRobustModifiers(modifiers)
}

// IsEnabled reports whether the defense is active in this profile.
func (p Profile) IsEnabled(defense Defense) bool {
	return !slices.Contains(p.disabled, defense)
}

// IsRobust reports whether every defense is enabled.
func (p Profile) IsRobust() bool {
	return len(p.disabled) == 0
}

// String returns the canonical form of the profile, which ParseProfile accepts.
func (p Profile) String() string {
	if p.IsRobust() {
		return robustKeyword
	}

	isNaive := len(p.disabled) == len(all)
	if isNaive {
		return naiveKeyword
	}

	parts := []string{robustKeyword}
	for _, defense := range p.disabled {
		parts = append(parts, disablePrefix+string(defense))
	}

	return strings.Join(parts, profileSeparator)
}

func parseNaive(modifiers []string) (Profile, error) {
	hasModifiers := len(modifiers) > 0
	if hasModifiers {
		return Profile{}, ErrNaiveWithModifier
	}

	return Profile{disabled: All()}, nil
}

func parseRobustModifiers(modifiers []string) (Profile, error) {
	disabled := make([]Defense, 0, len(modifiers))

	for _, modifier := range modifiers {
		defense, err := parseModifier(modifier)
		if err != nil {
			return Profile{}, err
		}

		if slices.Contains(disabled, defense) {
			return Profile{}, fmt.Errorf("%w: %q", ErrDuplicateDefense, defense)
		}

		disabled = append(disabled, defense)
	}

	slices.SortFunc(disabled, compareCatalogOrder)

	return Profile{disabled: disabled}, nil
}

func parseModifier(modifier string) (Defense, error) {
	trimmed := strings.TrimSpace(modifier)
	name, hasPrefix := strings.CutPrefix(trimmed, disablePrefix)
	if !hasPrefix {
		return "", fmt.Errorf("%w: %q", ErrMissingPrefix, trimmed)
	}

	defense := Defense(name)
	if !slices.Contains(all, defense) {
		return "", fmt.Errorf("%w: %q", ErrUnknownDefense, name)
	}

	return defense, nil
}

func compareCatalogOrder(a, b Defense) int {
	return slices.Index(all, a) - slices.Index(all, b)
}

// Notes explains the combinations where one defense depends on another that
// is off (docs/architecture.md, section 8.2, step 3); /internal/health shows them.
func (p Profile) Notes() []string {
	var notes []string

	isLockingWithoutTransaction := p.IsEnabled(BalanceLocking) && !p.IsEnabled(AtomicLedger)
	if isLockingWithoutTransaction {
		notes = append(notes, "balance_locking without atomic_ledger: each row lock ends with its own statement, so it serializes nothing")
	}

	isKeyWithoutTransaction := p.IsEnabled(Idempotency) && !p.IsEnabled(AtomicLedger)
	if isKeyWithoutTransaction {
		notes = append(notes, "idempotency without atomic_ledger: the key cannot share a commit with the effect, so the naive variant runs")
	}

	isOutboxWithoutTransaction := p.IsEnabled(Outbox) && !p.IsEnabled(AtomicLedger)
	if isOutboxWithoutTransaction {
		notes = append(notes, "outbox without atomic_ledger: the message and the transfer commit apart, so a crash between them still loses the message")
	}

	isInboxWithoutTransaction := p.IsEnabled(Inbox) && !p.IsEnabled(AtomicLedger)
	if isInboxWithoutTransaction {
		notes = append(notes, "inbox without atomic_ledger: an event's effect and its mark commit apart, so a crash between them applies it twice")
	}

	isFixedIDWithoutOutbox := p.IsEnabled(PartnerIdempotency) && !p.IsEnabled(Outbox)
	if isFixedIDWithoutOutbox {
		notes = append(notes, "partner_idempotency without outbox: the naive relay picks a new end_to_end_id per attempt anyway")
	}

	isRecoveryWithoutGuards := p.IsEnabled(Recovery) && !p.IsEnabled(StateMachine)
	if isRecoveryWithoutGuards {
		notes = append(notes, "recovery without state_machine: the sweeper walks to the partner's status by overwriting, with no guard against a concurrent webhook")
	}

	isRetryWithoutOutbox := p.IsEnabled(ResilientClient) && !p.IsEnabled(Outbox)
	if isRetryWithoutOutbox {
		notes = append(notes, "resilient_client without outbox: the naive relay has no message to reschedule, so a doubtful send still fails")
	}

	return notes
}
