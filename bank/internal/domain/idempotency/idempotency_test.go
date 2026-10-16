package idempotency_test

import (
	"errors"
	"strings"
	"testing"

	"centavo/bank/internal/domain/idempotency"
)

func TestParseKey(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want error
	}{
		{name: "operation id from the lab", raw: "r0142-p07-000318", want: nil},
		{name: "all allowed characters", raw: "Aa0._:-z", want: nil},
		{name: "at the upper limit", raw: strings.Repeat("k", 128), want: nil},
		{name: "missing", raw: "", want: idempotency.ErrKeyMissing},
		{name: "too short", raw: "short", want: idempotency.ErrKeyInvalid},
		{name: "too long", raw: strings.Repeat("k", 129), want: idempotency.ErrKeyInvalid},
		{name: "space", raw: "has space in it", want: idempotency.ErrKeyInvalid},
		{name: "newline injection", raw: "r0142-p07\n000318", want: idempotency.ErrKeyInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, err := idempotency.ParseKey(tc.raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}

			isAccepted := tc.want == nil
			if isAccepted && string(key) != tc.raw {
				t.Fatalf("key %q came back as %q", tc.raw, key)
			}
		})
	}
}
