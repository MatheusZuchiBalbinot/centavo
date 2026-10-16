package defenses_test

import (
	"errors"
	"slices"
	"testing"

	"centavo/bank/internal/platform/defenses"

	"pgregory.net/rapid"
)

func TestParseProfile_ValidProfiles(t *testing.T) {
	testCases := []struct {
		raw          string
		wantString   string
		wantDisabled []defenses.Defense
	}{
		{raw: "robust", wantString: "robust"},
		{raw: " robust ", wantString: "robust"},
		{raw: "naive", wantString: "naive", wantDisabled: defenses.All()},
		{raw: "robust,-idempotency", wantString: "robust,-idempotency", wantDisabled: []defenses.Defense{defenses.Idempotency}},
		{raw: "robust, -state_machine, -inbox", wantString: "robust,-inbox,-state_machine", wantDisabled: []defenses.Defense{defenses.Inbox, defenses.StateMachine}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.raw, func(t *testing.T) {
			profile, err := defenses.ParseProfile(testCase.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if profile.String() != testCase.wantString {
				t.Fatalf("String() = %q, want %q", profile.String(), testCase.wantString)
			}

			for _, defense := range defenses.All() {
				isExpectedDisabled := slices.Contains(testCase.wantDisabled, defense)
				if profile.IsEnabled(defense) == isExpectedDisabled {
					t.Fatalf("IsEnabled(%s) = %v, want %v", defense, profile.IsEnabled(defense), !isExpectedDisabled)
				}
			}
		})
	}
}

func TestParseProfile_InvalidProfiles(t *testing.T) {
	testCases := []struct {
		raw     string
		wantErr error
	}{
		{raw: "", wantErr: defenses.ErrEmptyProfile},
		{raw: "strong", wantErr: defenses.ErrUnknownBase},
		{raw: "naive,-outbox", wantErr: defenses.ErrNaiveWithModifier},
		{raw: "robust,outbox", wantErr: defenses.ErrMissingPrefix},
		{raw: "robust,-telepathy", wantErr: defenses.ErrUnknownDefense},
		{raw: "robust,-outbox,-outbox", wantErr: defenses.ErrDuplicateDefense},
		{raw: "robust,", wantErr: defenses.ErrMissingPrefix},
	}

	for _, testCase := range testCases {
		t.Run(testCase.raw, func(t *testing.T) {
			_, err := defenses.ParseProfile(testCase.raw)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestProfile_IsRobust(t *testing.T) {
	if !defenses.Robust().IsRobust() {
		t.Fatal("Robust() must be robust")
	}

	profile, err := defenses.ParseProfile("robust,-fencing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if profile.IsRobust() {
		t.Fatal("a profile with a disabled defense must not be robust")
	}
}

func TestProfile_AnyDisabledSet_RoundTripsThroughCanonicalString(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		disabled := rapid.SliceOfNDistinct(rapid.SampledFrom(defenses.All()), 0, -1, rapid.ID).Draw(t, "disabled")

		raw := "robust"
		for _, defense := range disabled {
			raw += ",-" + string(defense)
		}

		profile, err := defenses.ParseProfile(raw)
		if err != nil {
			t.Fatalf("ParseProfile(%q): %v", raw, err)
		}

		reparsed, err := defenses.ParseProfile(profile.String())
		if err != nil {
			t.Fatalf("ParseProfile(String()) = %v for %q", err, profile.String())
		}

		for _, defense := range defenses.All() {
			isExpectedEnabled := !slices.Contains(disabled, defense)

			hasWrongState := profile.IsEnabled(defense) != isExpectedEnabled || reparsed.IsEnabled(defense) != isExpectedEnabled
			if hasWrongState {
				t.Fatalf("defense %s: enabled = %v, reparsed = %v, want %v", defense, profile.IsEnabled(defense), reparsed.IsEnabled(defense), isExpectedEnabled)
			}
		}
	})
}
