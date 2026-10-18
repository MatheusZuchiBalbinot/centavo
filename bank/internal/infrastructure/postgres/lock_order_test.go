package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"centavo/bank/internal/application"
	"centavo/bank/internal/domain/ledger"

	"centavo/bank/internal/domain/transfer"
)

// TestLockOrder_SystemBalancesBeforeTheCustomer covers the writes that share
// a system balance with a step that then takes a customer: a refund moves
// transit then the customer, a return the partner mirror then the customer.
// Taking the customer first made a circle; the chaos rounds hit it several
// times a minute, and the reconciler, whose batch holds system balances,
// could not finish its last run. Deposits are not here: only deposits touch
// sys_external_cash, so taking it last can never close a circle.
//
// Each case holds the system account a write needs, as a refund or a return
// would, lets the write start and then asks for the customer. A write that
// took the customer first is now waiting on the system account while holding
// what the other side asks for: a deadlock.
func TestLockOrder_SystemBalancesBeforeTheCustomer(t *testing.T) {
	cases := []struct {
		name   string
		system ledger.AccountID
		write  func(t *testing.T, p pixBank, account ledger.AccountID) func(context.Context) error
	}{
		{name: "pix out reserve", system: ledger.PixOutTransitAccountID(), write: reservePixOut},
		{name: "incoming credit", system: ledger.PartnerSettlementAccountID(), write: creditIncomingPix},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newPixBank(t)
			account := fixture.bank.newCustomer(t)
			fixture.bank.fund(t, account, 10_000)
			write := tc.write(t, fixture, account)

			other, err := fixture.bank.pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer other.Rollback(context.Background()) //nolint:errcheck // a no-op after the commit below

			_, err = other.Exec(t.Context(), `UPDATE account_balances SET updated_at = now() WHERE account_id = $1`, uuid.UUID(tc.system))
			if err != nil {
				t.Fatal(err)
			}

			written := make(chan error, 1)

			go func() {
				written <- write(context.Background())
			}()

			waitForALockWait(t, fixture)

			_, err = other.Exec(t.Context(), `UPDATE account_balances SET updated_at = now() WHERE account_id = $1`, uuid.UUID(account))
			if err != nil {
				t.Fatalf("the other writer could not take the customer: %v", err)
			}

			err = other.Commit(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			err = <-written
			if err != nil {
				t.Fatalf("the write failed: %v", err)
			}
		})
	}
}

func reservePixOut(t *testing.T, fixture pixBank, account ledger.AccountID) func(context.Context) error {
	key := newKey()
	input := application.PixOutInput{AccountID: account, Amount: amountOf(t, 1_000), DestinationKey: "dest-0001", Write: application.WriteContext{Key: key, CorrelationID: string(key)}}

	return func(ctx context.Context) error {
		return fixture.unitOfWork.Run(ctx, func(ctx context.Context, stores application.Stores) error {
			operationID, err := ledger.NewOperationID()
			if err != nil {
				return err
			}

			_, err = fixture.create.Apply(ctx, stores, operationID, input)

			return err
		})
	}
}

func creditIncomingPix(_ *testing.T, fixture pixBank, account ledger.AccountID) func(context.Context) error {
	event := application.PartnerEvent{ID: "evt_" + uuid.NewString(), Type: application.EventIncoming, EndToEndID: transfer.EndToEndID("E" + uuid.NewString()), AmountCents: 700, AccountID: account.String()}

	return func(ctx context.Context) error {
		return fixture.webhook.Apply(ctx, event)
	}
}
