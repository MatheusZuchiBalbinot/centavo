// Package transfer is the PIX out state machine (docs/domain.md, section 8).
package transfer

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// ID identifies an external transfer.
type ID uuid.UUID

// Status is one state of an external transfer; states are exclusive.
type Status string

const (
	StatusCreated   Status = "created"
	StatusSent      Status = "sent"
	StatusConfirmed Status = "confirmed"
	StatusFailed    Status = "failed"
	StatusReversed  Status = "reversed"
)

// Posting is the ledger transaction a transition requires.
type Posting string

const (
	PostingNone   Posting = "none"
	PostingSettle Posting = "pix_out_settle"
	PostingRefund Posting = "pix_out_refund"
	PostingReturn Posting = "pix_out_return"
)

// Transition is a move between two states.
type Transition struct {
	From Status
	To   Status
}

// allowedTransitions is the state machine. The database has its own copy
// (external_transfer_allowed_transitions) and the checker a third, written
// apart from both.
var allowedTransitions = map[Transition]Posting{
	{From: StatusCreated, To: StatusSent}:       PostingNone,
	{From: StatusCreated, To: StatusConfirmed}:  PostingSettle,
	{From: StatusCreated, To: StatusFailed}:     PostingRefund,
	{From: StatusSent, To: StatusConfirmed}:     PostingSettle,
	{From: StatusSent, To: StatusFailed}:        PostingRefund,
	{From: StatusConfirmed, To: StatusReversed}: PostingReturn,
}

// destinationKeyPattern bounds what a client may send as a PIX key.
var destinationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._@+-]{3,77}$`)

var (
	ErrInvalidStateTransition = errors.New("invalid state transition")
	ErrInvalidDestinationKey  = errors.New("destination key must have 3 to 77 letters, digits or . _ @ + -")
	ErrTransferNotFound       = errors.New("external transfer not found")
)

// PostingOf returns the ledger transaction a transition requires, refusing
// transitions the state machine does not allow.
func PostingOf(from, to Status) (Posting, error) {
	posting, isAllowed := allowedTransitions[Transition{From: from, To: to}]
	if !isAllowed {
		return PostingNone, fmt.Errorf("%w: %s to %s", ErrInvalidStateTransition, from, to)
	}

	return posting, nil
}

// NewID generates a time-ordered transfer id.
func NewID() (ID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return ID{}, fmt.Errorf("generating transfer id: %w", err)
	}

	return ID(id), nil
}

// ParseID reads a transfer id from its canonical text form. Text that is not
// a UUID answers ErrTransferNotFound on purpose: it names no transfer, and a
// caller probing ids learns nothing from a different error.
func ParseID(raw string) (ID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return ID{}, ErrTransferNotFound
	}

	return ID(id), nil
}

func (id ID) String() string { return uuid.UUID(id).String() }

// EndToEndID is the transfer's id at the partner: E followed by the 32 hex
// digits of the transfer id (docs/domain.md, section 13.5).
func (id ID) EndToEndID() string {
	return "E" + strings.ReplaceAll(id.String(), "-", "")
}

// AttemptEndToEndID is what the naive relay sends: a new id per attempt,
// E<id>-<attempt>, so the partner sees each retry as another PIX.
func (id ID) AttemptEndToEndID(attempt int) string {
	return fmt.Sprintf("%s-%d", id.EndToEndID(), attempt)
}

// ParseDestinationKey validates a PIX key.
func ParseDestinationKey(raw string) (string, error) {
	if !destinationKeyPattern.MatchString(raw) {
		return "", ErrInvalidDestinationKey
	}

	return raw, nil
}
