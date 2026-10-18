package transfer

import (
	"errors"
	"fmt"
)

// The statuses a PIX can have at the partner.
const (
	PartnerProcessing PartnerStatus = "processing"
	PartnerSettled    PartnerStatus = "settled"
	PartnerRejected   PartnerStatus = "rejected"
	PartnerReturned   PartnerStatus = "returned"
	PartnerNotFound   PartnerStatus = "not_found"
)

var (
	// ErrStatusConflict is a bank status the partner's cannot follow from; it
	// is never resolved automatically.
	ErrStatusConflict = errors.New("bank and partner disagree on a transfer's status")
	// ErrSentButUnknown is a transfer the bank saw accepted and the partner
	// does not know: an anomaly to alert on, while the outbox sends again.
	ErrSentButUnknown = errors.New("transfer sent but unknown to the partner")
)

// paths is the table: the shortest valid path from the bank's status to the
// partner's. A missing cell is a conflict.
var paths = map[pathKey][]Status{
	{StatusCreated, PartnerProcessing}: {StatusSent},
	{StatusCreated, PartnerSettled}:    {StatusConfirmed},
	{StatusCreated, PartnerRejected}:   {StatusFailed},
	{StatusCreated, PartnerReturned}:   {StatusConfirmed, StatusReversed},
	{StatusCreated, PartnerNotFound}:   {},

	{StatusSent, PartnerProcessing}: {},
	{StatusSent, PartnerSettled}:    {StatusConfirmed},
	{StatusSent, PartnerRejected}:   {StatusFailed},
	{StatusSent, PartnerReturned}:   {StatusConfirmed, StatusReversed},

	{StatusConfirmed, PartnerSettled}:  {},
	{StatusConfirmed, PartnerReturned}: {StatusReversed},

	{StatusFailed, PartnerRejected}: {},

	{StatusReversed, PartnerReturned}: {},
}

// PartnerStatus is the authoritative status of a PIX at the partner.
type PartnerStatus string

// pathKey is one cell of the paths table below.
type pathKey struct {
	current       Status
	authoritative PartnerStatus
}

// PathTo returns the statuses to walk, in order, from current to where the
// partner's authoritative status puts the transfer. Each step is an allowed
// transition with its own posting; an empty path means nothing to do.
func PathTo(current Status, authoritative PartnerStatus) ([]Status, error) {
	isSentButUnknown := current == StatusSent && authoritative == PartnerNotFound
	if isSentButUnknown {
		return nil, ErrSentButUnknown
	}

	path, isKnown := paths[pathKey{current: current, authoritative: authoritative}]
	if !isKnown {
		return nil, fmt.Errorf("%w: bank %s, partner %s", ErrStatusConflict, current, authoritative)
	}

	return append([]Status{}, path...), nil
}
