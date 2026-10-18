// Package transfer is the PIX out state machine.
package transfer

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// The states of an external transfer.
const (
	StatusCreated   Status = "created"
	StatusSent      Status = "sent"
	StatusConfirmed Status = "confirmed"
	StatusFailed    Status = "failed"
	StatusReversed  Status = "reversed"
)

// The ledger transactions a transition can require.
const (
	PostingNone   Posting = "none"
	PostingSettle Posting = "pix_out_settle"
	PostingRefund Posting = "pix_out_refund"
	PostingReturn Posting = "pix_out_return"
)

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

// Errors of creating or moving an external transfer.
var (
	ErrInvalidStateTransition = errors.New("invalid state transition")
	ErrInvalidDestinationKey  = errors.New("destination key must have 3 to 77 letters, digits or . _ @ + -")
	ErrTransferNotFound       = errors.New("external transfer not found")
)

// ID identifies an external transfer.
type ID uuid.UUID

// Status is one state of an external transfer; states are exclusive.
type Status string

// Posting is the ledger transaction a transition requires.
type Posting string

// Transition is a move between two states.
type Transition struct {
	From Status
	To   Status
}

// EndToEndID is a PIX's id at the partner. Ours are E followed by the 32 hex
// digits of the transfer id; incoming ones are whatever the partner sent.
type EndToEndID string

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

func (e EndToEndID) String() string { return string(e) }

// EndToEndID is the transfer's id at the partner.
func (id ID) EndToEndID() EndToEndID {
	return EndToEndID("E" + strings.ReplaceAll(id.String(), "-", ""))
}

// AttemptEndToEndID is what the naive relay sends: a new id per attempt,
// E<id>-<attempt>, so the partner sees each retry as another PIX.
func (id ID) AttemptEndToEndID(attempt int) EndToEndID {
	attemptID := fmt.Sprintf("%s-%d", id.EndToEndID(), attempt)

	return EndToEndID(attemptID)
}

// ParseDestinationKey validates a PIX key.
func ParseDestinationKey(raw string) (string, error) {
	if !destinationKeyPattern.MatchString(raw) {
		return "", ErrInvalidDestinationKey
	}

	return raw, nil
}
