package seed_test

import (
	"errors"
	"testing"

	"centavo/lab/internal/seed"
)

func TestParse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want seed.Seed
		err  error
	}{
		{raw: "0x5eed0142", want: 0x5eed0142},
		{raw: "0X5EED", want: 0x5eed},
		{raw: "42", want: 42},
		{raw: "0xffffffffffffffff", want: 0xffffffffffffffff},
		{raw: "0x1ffffffffffffffff", err: seed.ErrInvalidSeed},
		{raw: "seed", err: seed.ErrInvalidSeed},
		{raw: "", err: seed.ErrInvalidSeed},
	}

	for _, tc := range cases {
		got, err := seed.Parse(tc.raw)
		isUnexpected := !errors.Is(err, tc.err) || got != tc.want
		if isUnexpected {
			t.Errorf("Parse(%q) = %v, %v; want %v, %v", tc.raw, got, err, tc.want, tc.err)
		}
	}
}

func TestString_RoundTrips(t *testing.T) {
	t.Parallel()

	original := seed.Seed(0x5eed0142)

	parsed, err := seed.Parse(original.String())
	if err != nil {
		t.Fatalf("round trip of %s: %v, %v", original, parsed, err)
	}

	if parsed != original {
		t.Fatalf("round trip of %s: %v, %v", original, parsed, err)
	}
}

func TestStream_IsReproducibleAndIndependent(t *testing.T) {
	t.Parallel()

	round := seed.Seed(0x5eed0142)

	first := sample(round, "workload.ops/1")
	again := sample(round, "workload.ops/1")
	other := sample(round, "workload.ops/2")
	otherSeed := sample(round+1, "workload.ops/1")

	if first != again {
		t.Fatal("the same seed and stream name gave different sequences")
	}

	isUnexpected := first == other || first == otherSeed
	if isUnexpected {
		t.Fatal("different streams or seeds gave the same sequence")
	}
}

func sample(round seed.Seed, name string) [64]uint64 {
	stream := round.Stream(name)

	var values [64]uint64
	for i := range values {
		values[i] = stream.Uint64()
	}

	return values
}
