package money_test

import (
	"errors"
	"math"
	"testing"

	"pgregory.net/rapid"

	"centavo/bank/internal/domain/money"
)

func TestNewAmount_AcceptsExactlyTheValidRange(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		cents := money.Cents(rapid.Int64().Draw(t, "cents"))

		amount, err := money.NewAmount(cents)

		isValid := cents > 0 && cents <= money.MaxOperationCents
		if !isValid {
			if err == nil {
				t.Fatalf("accepted invalid amount %d", cents)
			}

			return
		}

		if err != nil {
			t.Fatalf("rejected valid amount %d: %v", cents, err)
		}

		if amount.Cents() != cents || amount.Negated() != -cents {
			t.Fatalf("amount %d round-tripped as %d and %d", cents, amount.Cents(), amount.Negated())
		}
	})
}

func TestNewAmount_ReportsWhichRuleFailed(t *testing.T) {
	cases := []struct {
		name  string
		cents money.Cents
		want  error
	}{
		{name: "zero", cents: 0, want: money.ErrAmountNotPositive},
		{name: "negative", cents: -1, want: money.ErrAmountNotPositive},
		{name: "above limit", cents: money.MaxOperationCents + 1, want: money.ErrAmountAboveLimit},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := money.NewAmount(tc.cents)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestBalanceAdd_DetectsEveryOverflow(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		start := rapid.Int64().Draw(t, "start")
		delta := rapid.Int64().Draw(t, "delta")

		sum, err := money.NewBalance(money.Cents(start)).Add(money.Cents(delta))

		wouldOverflow := (delta > 0 && start > math.MaxInt64-delta) || (delta < 0 && start < math.MinInt64-delta)
		if wouldOverflow {
			if !errors.Is(err, money.ErrBalanceOverflow) {
				t.Fatalf("%d + %d overflowed without error (got %v)", start, delta, err)
			}

			return
		}

		if err != nil {
			t.Fatalf("%d + %d: unexpected error %v", start, delta, err)
		}

		if sum.Cents() != money.Cents(start+delta) {
			t.Fatalf("%d + %d = %d", start, delta, sum.Cents())
		}
	})
}

func TestBalanceCovers_ComparesWithTheAmount(t *testing.T) {
	amount, err := money.NewAmount(100)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		balance money.Cents
		want    bool
	}{
		{balance: 99, want: false},
		{balance: 100, want: true},
		{balance: 101, want: true},
		{balance: -5, want: false},
	}

	for _, tc := range cases {
		got := money.NewBalance(tc.balance).Covers(amount)
		if got != tc.want {
			t.Errorf("balance %d covers 100: got %v, want %v", tc.balance, got, tc.want)
		}
	}
}
