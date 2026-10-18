package checker

import (
	"testing"

	"centavo/lab/internal/snapshot"
)

func TestImmutability(t *testing.T) {
	t.Parallel()

	before := snapshot.LedgerState{FirstTransactionID: "tx-first", Bands: []snapshot.Band{
		{Band: 0, Count: 100_000, SumCents: 0, Hash: 42},
		{Band: 1, Count: 1_200, SumCents: 0, Hash: 7},
	}}

	cases := []struct {
		name       string
		first      string
		bands      []snapshot.Band
		shouldFail bool
	}{
		{name: "untouched, the open band grew", first: "tx-first", bands: []snapshot.Band{before.Bands[0], {Band: 1, Count: 9_000, Hash: 99}}},
		{name: "an old entry changed", first: "tx-first", bands: []snapshot.Band{{Band: 0, Count: 100_000, Hash: 43}, before.Bands[1]}, shouldFail: true},
		{name: "old entries vanished", first: "tx-first", bands: []snapshot.Band{before.Bands[1]}, shouldFail: true},
		{name: "a rebuilt database", first: "tx-other", bands: []snapshot.Band{{Band: 0, Count: 10}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := newRoundBuilder(t)
			s := b.snapshot()
			s.Bands = tc.bands
			s.Meta.FirstTransactionID = tc.first

			result := evaluate(indexOperations(b.records), s, outsideSources{previous: &before})
			isBroken := len(rulesOf(result)) == 1 && rulesOf(result)[0] == RuleI8Bands

			if isBroken != tc.shouldFail {
				t.Fatalf("violations %+v, want I8 broken: %v", result.Violations, tc.shouldFail)
			}
		})
	}
}
