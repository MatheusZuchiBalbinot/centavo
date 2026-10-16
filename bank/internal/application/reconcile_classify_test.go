package application

import (
	"testing"

	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

func transferIn(t *testing.T, status transfer.Status, cents money.Cents) ExternalTransfer {
	t.Helper()

	amount, err := money.NewAmount(cents)
	if err != nil {
		t.Fatal(err)
	}

	return ExternalTransfer{Status: status, Amount: amount}
}

func TestClassify(t *testing.T) {
	settlement := StatementEntry{Seq: 7, Kind: StatementSettlement, EndToEndID: "E1", AmountCents: 500}
	returned := StatementEntry{Seq: 8, Kind: StatementReturn, EndToEndID: "E1", AmountCents: 500}
	incoming := StatementEntry{Seq: 9, Kind: StatementIncoming, EndToEndID: "E2", AmountCents: 300, AccountID: "acc"}

	cases := []struct {
		name  string
		entry StatementEntry
		facts entryFacts
		want  verdict
	}{
		{
			name: "settlement of a confirmed transfer", entry: settlement,
			facts: entryFacts{hasTransfer: true, transfer: transferIn(t, transfer.StatusConfirmed, 500)},
			want:  verdict{divergence: DivergenceMatched, action: actionNone},
		},
		{
			name: "settlement whose webhook was lost", entry: settlement,
			facts: entryFacts{hasTransfer: true, transfer: transferIn(t, transfer.StatusSent, 500)},
			want:  verdict{divergence: DivergenceMissingSettlement, action: actionWalk, walkTo: transfer.PartnerSettled},
		},
		{
			name: "second settlement of the same PIX", entry: settlement,
			facts: entryFacts{hasTransfer: true, transfer: transferIn(t, transfer.StatusConfirmed, 500), priorItems: 1},
			want:  verdict{divergence: DivergenceDuplicate, action: actionSuspense, suspenseCents: 500},
		},
		{
			name: "settlement the bank refunded", entry: settlement,
			facts: entryFacts{hasTransfer: true, transfer: transferIn(t, transfer.StatusFailed, 500)},
			want:  verdict{divergence: DivergenceMissingSettlement, action: actionSuspense, suspenseCents: 500},
		},
		{
			name: "settlement of an unknown PIX", entry: settlement,
			facts: entryFacts{},
			want:  verdict{divergence: DivergenceUnknownEntry, action: actionSuspense, suspenseCents: 500},
		},
		{
			name: "settlement of another amount", entry: settlement,
			facts: entryFacts{hasTransfer: true, transfer: transferIn(t, transfer.StatusSent, 450)},
			want:  verdict{divergence: DivergenceAmountMismatch, action: actionWalkAndSuspense, walkTo: transfer.PartnerSettled, suspenseCents: 50},
		},
		{
			name: "return the bank missed", entry: returned,
			facts: entryFacts{hasTransfer: true, transfer: transferIn(t, transfer.StatusConfirmed, 500)},
			want:  verdict{divergence: DivergenceMissingReturn, action: actionWalk, walkTo: transfer.PartnerReturned},
		},
		{
			name: "return already booked", entry: returned,
			facts: entryFacts{hasTransfer: true, transfer: transferIn(t, transfer.StatusReversed, 500)},
			want:  verdict{divergence: DivergenceMatched, action: actionNone},
		},
		{
			name: "incoming never credited", entry: incoming,
			facts: entryFacts{isAccountKnown: true},
			want:  verdict{divergence: DivergenceMissingIncoming, action: actionCredit},
		},
		{
			name: "incoming for an unknown account", entry: incoming,
			facts: entryFacts{},
			want:  verdict{divergence: DivergenceUnknownEntry, action: actionSuspense, suspenseCents: -300},
		},
		{
			name: "incoming already credited", entry: incoming,
			facts: entryFacts{isCredited: true, isAccountKnown: true},
			want:  verdict{divergence: DivergenceMatched, action: actionNone},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.entry, tc.facts)
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
