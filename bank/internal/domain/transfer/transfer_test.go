package transfer_test

import (
	"errors"
	"strings"
	"testing"

	"centavo/bank/internal/domain/transfer"
)

func TestPostingOf_FollowsTheStateMachine(t *testing.T) {
	allowed := map[[2]transfer.Status]transfer.Posting{
		{transfer.StatusCreated, transfer.StatusSent}:       transfer.PostingNone,
		{transfer.StatusCreated, transfer.StatusConfirmed}:  transfer.PostingSettle,
		{transfer.StatusCreated, transfer.StatusFailed}:     transfer.PostingRefund,
		{transfer.StatusSent, transfer.StatusConfirmed}:     transfer.PostingSettle,
		{transfer.StatusSent, transfer.StatusFailed}:        transfer.PostingRefund,
		{transfer.StatusConfirmed, transfer.StatusReversed}: transfer.PostingReturn,
	}

	statuses := []transfer.Status{transfer.StatusCreated, transfer.StatusSent, transfer.StatusConfirmed, transfer.StatusFailed, transfer.StatusReversed}

	for _, from := range statuses {
		for _, to := range statuses {
			posting, err := transfer.PostingOf(from, to)
			want, isAllowed := allowed[[2]transfer.Status{from, to}]

			if isAllowed && (err != nil || posting != want) {
				t.Errorf("%s to %s: got %s, %v; want %s", from, to, posting, err, want)
			}

			if !isAllowed && !errors.Is(err, transfer.ErrInvalidStateTransition) {
				t.Errorf("%s to %s should be refused, got %s, %v", from, to, posting, err)
			}
		}
	}
}

func TestEndToEndIDs(t *testing.T) {
	id, err := transfer.NewID()
	if err != nil {
		t.Fatal(err)
	}

	e2e := id.EndToEndID()
	isFixed := strings.HasPrefix(e2e, "E") && len(e2e) == 33 && !strings.Contains(e2e, "-")
	if !isFixed {
		t.Fatalf("end_to_end_id %q", e2e)
	}

	if id.AttemptEndToEndID(2) != e2e+"-2" {
		t.Fatalf("attempt id %q", id.AttemptEndToEndID(2))
	}
}

func TestParseDestinationKey(t *testing.T) {
	for _, key := range []string{"dest-0031", "ana@example.com", "+5511999990000"} {
		if _, err := transfer.ParseDestinationKey(key); err != nil {
			t.Errorf("%q refused: %v", key, err)
		}
	}

	for _, key := range []string{"", "x", "two words", strings.Repeat("k", 78)} {
		if _, err := transfer.ParseDestinationKey(key); !errors.Is(err, transfer.ErrInvalidDestinationKey) {
			t.Errorf("%q accepted", key)
		}
	}
}

// FuzzParseDestinationKey: an accepted key is the input, 3 to 77 characters
// long, all of them in the allowed set.
func FuzzParseDestinationKey(f *testing.F) {
	for _, seed := range []string{"", "ab", "dest-0001", "user@example.com", "+5511999999999", "a b c"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		key, err := transfer.ParseDestinationKey(raw)
		if err != nil {
			if !errors.Is(err, transfer.ErrInvalidDestinationKey) {
				t.Fatalf("refused %q with %v", raw, err)
			}

			return
		}

		isAllowed := key == raw && len(key) >= 3 && len(key) <= 77 && strings.Trim(key, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._@+-") == ""
		if !isAllowed {
			t.Fatalf("accepted %q", raw)
		}
	})
}
