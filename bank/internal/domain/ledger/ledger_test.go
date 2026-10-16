package ledger_test

import (
	"errors"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

func TestPositionDebit_NeverOverdrawsAndAdvancesTheSequence(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		balance := money.Cents(rapid.Int64Range(-1_000, 2*int64(money.MaxOperationCents)).Draw(t, "balance"))
		lastSeq := ledger.Seq(rapid.Int64Range(0, 1_000_000).Draw(t, "lastSeq"))
		amount, err := money.NewAmount(money.Cents(rapid.Int64Range(1, int64(money.MaxOperationCents)).Draw(t, "amount")))
		if err != nil {
			t.Fatal(err)
		}

		position := ledger.Position{Balance: money.NewBalance(balance), LastSeq: lastSeq}
		next, err := position.Debit(amount)

		isCovered := balance >= amount.Cents()
		if !isCovered {
			if !errors.Is(err, ledger.ErrInsufficientFunds) {
				t.Fatalf("debit of %d from %d: got %v, want ErrInsufficientFunds", amount.Cents(), balance, err)
			}

			return
		}

		if err != nil {
			t.Fatalf("debit of %d from %d: %v", amount.Cents(), balance, err)
		}

		if next.Balance.Cents() != balance-amount.Cents() || next.Balance.Cents() < 0 {
			t.Fatalf("debit of %d from %d left %d", amount.Cents(), balance, next.Balance.Cents())
		}

		if next.LastSeq != lastSeq+1 {
			t.Fatalf("sequence went from %d to %d", lastSeq, next.LastSeq)
		}
	})
}

func TestCheckTransferAccounts_RejectsTheSameAccount(t *testing.T) {
	first, err := ledger.NewAccountID()
	if err != nil {
		t.Fatal(err)
	}

	second, err := ledger.NewAccountID()
	if err != nil {
		t.Fatal(err)
	}

	err = ledger.CheckTransferAccounts(first, first)
	if !errors.Is(err, ledger.ErrSameAccount) {
		t.Fatalf("same account: got %v", err)
	}

	err = ledger.CheckTransferAccounts(first, second)
	if err != nil {
		t.Fatalf("distinct accounts: got %v", err)
	}
}

func TestParseHolderName(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		isValid bool
	}{
		{name: "trims spaces", raw: "  Ana  ", want: "Ana", isValid: true},
		{name: "accepts accents", raw: "João", want: "João", isValid: true},
		{name: "empty", raw: "", isValid: false},
		{name: "only spaces", raw: "   ", isValid: false},
		{name: "at the limit", raw: strings.Repeat("é", 120), want: strings.Repeat("é", 120), isValid: true},
		{name: "above the limit", raw: strings.Repeat("a", 121), isValid: false},
		{name: "invalid utf-8", raw: "a\xffb", isValid: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ledger.ParseHolderName(tc.raw)

			if !tc.isValid {
				if !errors.Is(err, ledger.ErrInvalidHolderName) {
					t.Fatalf("got %q, %v; want ErrInvalidHolderName", got, err)
				}

				return
			}

			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestParseAccountID_RoundTripsAndRejectsGarbage(t *testing.T) {
	id, err := ledger.NewAccountID()
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := ledger.ParseAccountID(id.String())
	if err != nil || parsed != id {
		t.Fatalf("round trip of %s: got %s, %v", id, parsed, err)
	}

	_, err = ledger.ParseAccountID("not-a-uuid")
	if !errors.Is(err, ledger.ErrInvalidAccountID) {
		t.Fatalf("garbage: got %v", err)
	}
}
