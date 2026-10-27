package transfer_test

import (
	"errors"
	"slices"
	"testing"

	"pgregory.net/rapid"

	"centavo/bank/internal/domain/transfer"
)

var (
	bankStatuses    = []transfer.Status{transfer.StatusCreated, transfer.StatusSent, transfer.StatusConfirmed, transfer.StatusFailed, transfer.StatusReversed}
	partnerStatuses = []transfer.PartnerStatus{transfer.PartnerProcessing, transfer.PartnerSettled, transfer.PartnerRejected, transfer.PartnerReturned, transfer.PartnerNotFound}
)

// partnerLifecycle is how the partner's status moves, written apart from the
// bank's table: processing, then settled or rejected; settled may be returned.
var partnerLifecycle = map[transfer.PartnerStatus][]transfer.PartnerStatus{
	transfer.PartnerNotFound:   {transfer.PartnerProcessing, transfer.PartnerRejected},
	transfer.PartnerProcessing: {transfer.PartnerSettled, transfer.PartnerRejected},
	transfer.PartnerSettled:    {transfer.PartnerReturned},
}

// matches says which bank statuses agree with a partner status once the
// bank caught up.
var matches = map[transfer.PartnerStatus][]transfer.Status{
	transfer.PartnerNotFound:   {transfer.StatusCreated},
	transfer.PartnerProcessing: {transfer.StatusSent},
	transfer.PartnerSettled:    {transfer.StatusConfirmed},
	transfer.PartnerRejected:   {transfer.StatusFailed},
	transfer.PartnerReturned:   {transfer.StatusReversed},
}

func TestPathTo_EveryStepIsAllowed(t *testing.T) {
	t.Parallel()

	for _, current := range bankStatuses {
		for _, authoritative := range partnerStatuses {
			path, err := transfer.PathTo(current, authoritative)
			if err != nil {
				continue
			}

			from := current

			for _, to := range path {
				_, err := transfer.PostingOf(from, to)
				if err != nil {
					t.Errorf("%s toward %s: step %s to %s is not allowed", current, authoritative, from, to)
				}

				from = to
			}
		}
	}
}

func TestPathTo_ConflictsAreNeverResolved(t *testing.T) {
	t.Parallel()

	conflicts := []struct {
		current       transfer.Status
		authoritative transfer.PartnerStatus
	}{
		{transfer.StatusConfirmed, transfer.PartnerRejected},
		{transfer.StatusFailed, transfer.PartnerSettled},
		{transfer.StatusReversed, transfer.PartnerProcessing},
		{transfer.StatusConfirmed, transfer.PartnerNotFound},
	}

	for _, c := range conflicts {
		_, err := transfer.PathTo(c.current, c.authoritative)
		if !errors.Is(err, transfer.ErrStatusConflict) {
			t.Errorf("%s with partner %s: got %v, want a conflict", c.current, c.authoritative, err)
		}
	}

	_, err := transfer.PathTo(transfer.StatusSent, transfer.PartnerNotFound)
	if !errors.Is(err, transfer.ErrSentButUnknown) {
		t.Errorf("sent but unknown: got %v", err)
	}
}

// The confluence property of the state machine: the partner moves its
// transfer along its lifecycle, the bank resyncs at arbitrary moments, and
// once the bank reads the final status it agrees with it.
func TestTransfer_AnyEventOrderWithDuplicates_ConvergesToAuthoritativeState(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		partner := transfer.PartnerNotFound
		bank := transfer.StatusCreated
		steps := rapid.IntRange(0, 12).Draw(t, "steps")

		for i := range steps {
			next := partnerLifecycle[partner]
			isMoving := len(next) > 0 && rapid.Bool().Draw(t, "partner moves")

			if isMoving {
				partner = rapid.SampledFrom(next).Draw(t, "partner status")
			}

			isResync := rapid.Bool().Draw(t, "bank resyncs")
			if isResync {
				bank = resync(t, bank, partner, i)
			}
		}

		bank = resync(t, bank, partner, steps)

		if !slices.Contains(matches[partner], bank) {
			t.Fatalf("partner %s, bank %s after a final resync", partner, bank)
		}
	})
}

func resync(t *rapid.T, bank transfer.Status, partner transfer.PartnerStatus, step int) transfer.Status {
	path, err := transfer.PathTo(bank, partner)
	if errors.Is(err, transfer.ErrSentButUnknown) {
		return bank
	}

	if err != nil {
		t.Fatalf("step %d: a partner that only moves forward caused %v", step, err)
	}

	for _, next := range path {
		bank = next
	}

	return bank
}
