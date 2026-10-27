package transfer

import (
	"errors"
	"fmt"
)

const (
	PartnerProcessing PartnerStatus = "processing"
	PartnerSettled    PartnerStatus = "settled"
	PartnerRejected   PartnerStatus = "rejected"
	PartnerReturned   PartnerStatus = "returned"
	PartnerNotFound   PartnerStatus = "not_found"
)

var (
	// Never resolved automatically.
	ErrStatusConflict = errors.New("bank and partner disagree on a transfer's status")
	// An anomaly to alert on, while the outbox sends again.
	ErrSentButUnknown = errors.New("transfer sent but unknown to the partner")
)

// The shortest valid path from the bank's status to the partner's; a
// missing cell is a conflict.
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

type PartnerStatus string

type pathKey struct {
	current       Status
	authoritative PartnerStatus
}

// Each step is an allowed transition with its own posting; an empty path
// means nothing to do.
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
