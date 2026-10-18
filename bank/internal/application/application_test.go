package application

import (
	"context"
	"slices"
	"testing"
	"time"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
)

// recordingWriter keeps the order balances were moved in.
type recordingWriter struct {
	moved []ledger.AccountID
}

// TestPostAll_PostsInAccountOrder: the order the caller lists the lines in
// does not matter, the balances move system accounts first, as every writer
// takes them.
func TestPostAll_PostsInAccountOrder(t *testing.T) {
	t.Parallel()

	customer, err := ledger.NewAccountID()
	if err != nil {
		t.Fatal(err)
	}

	writer := &recordingWriter{}
	lines := []posting{{account: customer, delta: 700}, {account: ledger.SuspenseAccountID(), delta: 1}, {account: ledger.PartnerSettlementAccountID(), delta: -701}}

	positions, err := poster{positions: writer, entries: writer}.postAll(t.Context(), ledger.TransactionID{}, lines)
	if err != nil {
		t.Fatal(err)
	}

	want := []ledger.AccountID{ledger.PartnerSettlementAccountID(), ledger.SuspenseAccountID(), customer}
	if !slices.Equal(writer.moved, want) {
		t.Fatalf("moved %v, want %v", writer.moved, want)
	}

	if len(positions) != len(lines) {
		t.Fatalf("got %d positions for %d lines", len(positions), len(lines))
	}
}

func (w *recordingWriter) OverwritePosition(context.Context, ledger.AccountID, ledger.Position) error {
	return nil
}

func (w *recordingWriter) IncrementBalance(_ context.Context, id ledger.AccountID, _ money.Cents) (ledger.Position, error) {
	w.moved = append(w.moved, id)

	return ledger.Position{LastSeq: ledger.Seq(len(w.moved))}, nil
}

func (w *recordingWriter) InsertTransaction(context.Context, ledger.Transaction) (time.Time, error) {
	return time.Time{}, nil
}

func (w *recordingWriter) InsertEntry(context.Context, ledger.Entry) error {
	return nil
}
