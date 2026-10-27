package transfer

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

const (
	StatusCreated   Status = "created"
	StatusSent      Status = "sent"
	StatusConfirmed Status = "confirmed"
	StatusFailed    Status = "failed"
	StatusReversed  Status = "reversed"
)

const (
	PostingNone   Posting = "none"
	PostingSettle Posting = "pix_out_settle"
	PostingRefund Posting = "pix_out_refund"
	PostingReturn Posting = "pix_out_return"
)

// The database has its own copy (external_transfer_allowed_transitions) and
// the checker a third, written apart from both.
var allowedTransitions = map[Transition]Posting{
	{From: StatusCreated, To: StatusSent}:       PostingNone,
	{From: StatusCreated, To: StatusConfirmed}:  PostingSettle,
	{From: StatusCreated, To: StatusFailed}:     PostingRefund,
	{From: StatusSent, To: StatusConfirmed}:     PostingSettle,
	{From: StatusSent, To: StatusFailed}:        PostingRefund,
	{From: StatusConfirmed, To: StatusReversed}: PostingReturn,
}

var destinationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._@+-]{3,77}$`)

var (
	ErrInvalidStateTransition = errors.New("invalid state transition")
	ErrInvalidDestinationKey  = errors.New("destination key must have 3 to 77 letters, digits or . _ @ + -")
	ErrTransferNotFound       = errors.New("external transfer not found")
)

type ID uuid.UUID

type Status string

type Posting string

type Transition struct {
	From Status
	To   Status
}

// Ours are E followed by the 32 hex digits of the transfer id; incoming ones
// are whatever the partner sent.
type EndToEndID string

func PostingOf(from, to Status) (Posting, error) {
	posting, isAllowed := allowedTransitions[Transition{From: from, To: to}]
	if !isAllowed {
		return PostingNone, fmt.Errorf("%w: %s to %s", ErrInvalidStateTransition, from, to)
	}

	return posting, nil
}

func NewID() (ID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return ID{}, fmt.Errorf("generating transfer id: %w", err)
	}

	return ID(id), nil
}

// Text that is not a UUID answers ErrTransferNotFound on purpose: it names
// no transfer, and a caller probing ids learns nothing from a different error.
func ParseID(raw string) (ID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return ID{}, ErrTransferNotFound
	}

	return ID(id), nil
}

func (id ID) String() string { return uuid.UUID(id).String() }

func (e EndToEndID) String() string { return string(e) }

func (id ID) EndToEndID() EndToEndID {
	return EndToEndID("E" + strings.ReplaceAll(id.String(), "-", ""))
}

// A new id per attempt, so the partner sees each retry as another PIX.
func (id ID) AttemptEndToEndID(attempt int) EndToEndID {
	attemptID := fmt.Sprintf("%s-%d", id.EndToEndID(), attempt)

	return EndToEndID(attemptID)
}

func ParseDestinationKey(raw string) (string, error) {
	if !destinationKeyPattern.MatchString(raw) {
		return "", ErrInvalidDestinationKey
	}

	return raw, nil
}
