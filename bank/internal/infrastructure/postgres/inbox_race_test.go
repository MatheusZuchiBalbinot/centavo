package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/transfer"
)

// takingPartner answers the resync's status query and, before it does, lets
// another processor take the event the failed apply has just freed.
type takingPartner struct {
	take func()
}

func (p takingPartner) Status(context.Context, transfer.EndToEndID) (application.AuthoritativeStatus, error) {
	p.take()

	return application.AuthoritativeStatus{Status: transfer.PartnerReturned}, nil
}

// TestInbox_ResyncLeavesAnEventAnotherProcessorTook reproduces the deadlock
// between the two inbox processors of the chaos rounds: a return that came
// before the settlement does not fit, its transaction rolls back and frees the
// event, and the resync asks the partner outside any transaction. Another processor
// takes the event meanwhile and goes for the transfer. A resync that locks the
// transfer and then marks the event waits on that processor, which waits on
// it until the lock timeout, and the event ends deferred on a lock error; one
// that first takes the event back leaves it to the other processor at once.
func TestInbox_ResyncLeavesAnEventAnotherProcessorTook(t *testing.T) {
	fixture := newOutboxBank(t)
	intake, processor := newInbox(fixture, statusAnswer(transfer.PartnerReturned))
	drainInbox(t, processor)

	created := fixture.sentTransfer(t, 4_000)
	again := application.PartnerEvent{ID: newEventID(), Type: application.EventReturned, EndToEndID: created.EndToEndID, AmountCents: 4_000}
	receive(t, intake, again)

	var other pgx.Tx

	takeTheEvent := func() {
		var err error

		other, err = fixture.bank.pool.Begin(context.Background())
		if err == nil {
			_, err = other.Exec(context.Background(), `SELECT id FROM inbox_events WHERE event_id = $1 FOR UPDATE`, again.ID)
		}

		if err != nil {
			panic(err)
		}
	}

	_, racing := newInbox(fixture, takingPartner{take: takeTheEvent})
	processed := make(chan error, 1)

	go func() {
		_, err := racing.RunOnce(context.Background())
		processed <- err
	}()

	// The fixed resync returns at once; the old one waits on the event.
	var (
		resyncErr  error
		isFinished bool
	)

	select {
	case resyncErr = <-processed:
		isFinished = true
	case <-time.After(2 * time.Second):
	}

	_, err := other.Exec(t.Context(), `SELECT id FROM external_transfers WHERE id = $1 FOR UPDATE`, uuid.UUID(created.TransferID))
	if err != nil {
		t.Fatalf("the other processor could not take the transfer: %v", err)
	}

	err = other.Commit(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if !isFinished {
		resyncErr = <-processed
	}

	if resyncErr != nil {
		t.Fatalf("the resync failed: %v", resyncErr)
	}

	if !isFinished {
		t.Fatal("the resync held the transfer and waited on the event another processor took")
	}

	var outcome string

	err = fixture.bank.pool.QueryRow(t.Context(), `SELECT coalesce(outcome, '') FROM inbox_events WHERE event_id = $1`, again.ID).Scan(&outcome)
	if err != nil {
		t.Fatalf("the event, still the other processor's, was marked %q (%v)", outcome, err)
	}

	if outcome != "" {
		t.Fatalf("the event, still the other processor's, was marked %q (%v)", outcome, err)
	}
}
