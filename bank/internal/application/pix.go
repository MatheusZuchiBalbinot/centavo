package application

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"centavo/bank/internal/domain/ledger"
	"centavo/bank/internal/domain/money"
	"centavo/bank/internal/domain/transfer"
)

// The naive PIX use cases here are the controls the robust ones are measured
// against: the publication is a NOTIFY that is lost when no worker listens, the
// relay sends a new end_to_end_id on every attempt and gives up on a timeout,
// and the webhook handler applies the effect directly, overwriting the status
// with whatever event arrives last. The robust variants are the outbox,
// partner_idempotency, inbox, state_machine, recovery and resilient_client
// defenses.

// Causes recorded in the transition history.
const (
	causeAPI     TransitionCause = "api"
	causeRelay   TransitionCause = "relay"
	causeWebhook TransitionCause = "webhook"
)

// Failure codes the bank records itself; a partner refusal can bring its own.
const (
	failurePartner = "partner_rejected"
	failureTimeout = "partner_timeout"
)

const firstRelayAttempt = 1

// Errors of the PIX use cases.
var (
	ErrUnknownEvent = errors.New("unknown partner event")
	// ErrAlreadyCredited means the incoming PIX was credited before (a PIX in
	// is credited once per end_to_end_id).
	ErrAlreadyCredited = errors.New("incoming PIX already credited")
)

// ExternalTransfer is a PIX out as stored.
type ExternalTransfer struct {
	ID             transfer.ID
	EndToEndID     transfer.EndToEndID
	OperationID    ledger.OperationID
	ClientRef      string
	AccountID      ledger.AccountID
	Amount         money.Amount
	DestinationKey string
	Status         transfer.Status
	StatusSeq      int
	FailureCode    string
	CreatedAt      time.Time
}

// TransitionRecord is one line of a transfer's state history.
type TransitionRecord struct {
	TransferID          transfer.ID
	Seq                 int
	From                *transfer.Status
	To                  transfer.Status
	Cause               TransitionCause
	LedgerTransactionID *ledger.TransactionID
	CorrelationID       string
	CreatedAt           time.Time
}

// StatusChange is the new status of a transfer.
type StatusChange struct {
	TransferID  transfer.ID
	To          transfer.Status
	FailureCode string
}

// IncomingRecord is a PIX in as stored.
type IncomingRecord struct {
	ID            uuid.UUID
	EndToEndID    transfer.EndToEndID
	AccountID     ledger.AccountID
	Amount        money.Amount
	SourceEventID string
}

// PixStore keeps external transfers, their transitions and incoming PIX.
type PixStore interface {
	InsertExternalTransfer(ctx context.Context, external ExternalTransfer) (time.Time, error)
	InsertTransition(ctx context.Context, transition TransitionRecord) error
	FindExternalTransfer(ctx context.Context, id transfer.ID) (ExternalTransfer, []TransitionRecord, error)
	FindExternalTransferByEndToEnd(ctx context.Context, endToEndID transfer.EndToEndID) (ExternalTransfer, error)
	// ClaimAttempt moves the transfer from one end_to_end_id to the next, so
	// only one relay sends each attempt.
	ClaimAttempt(ctx context.Context, id transfer.ID, seen, next transfer.EndToEndID) (bool, error)
	// OverwriteStatus sets the status without checking the current one and
	// returns what it was and the new sequence number.
	OverwriteStatus(ctx context.Context, change StatusChange) (transfer.Status, int, error)
	// LockExternalTransfer locks the transfer for the rest of the
	// transaction and returns it.
	LockExternalTransfer(ctx context.Context, id transfer.ID) (ExternalTransfer, error)
	// AdvanceStatus is the guarded compare-and-set: it changes the status
	// only if the sequence number is still the one read under the lock.
	AdvanceStatus(ctx context.Context, change StatusChange, seenSeq int) error
	InsertIncoming(ctx context.Context, incoming IncomingRecord) error
	// LockEndToEnd holds one PIX for the rest of the transaction, by its
	// end_to_end_id, before any of its rows exists: an incoming PIX has none
	// until it is credited.
	LockEndToEnd(ctx context.Context, endToEndID transfer.EndToEndID) error
	// LockTransfersOf locks the transfers of these end_to_end_ids, in id order.
	LockTransfersOf(ctx context.Context, endToEndIDs []transfer.EndToEndID) error
	// ListCreated returns transfers still in created, oldest first.
	ListCreated(ctx context.Context, limit int) ([]transfer.ID, error)
}

// PartnerAnswer is how the partner answered a submission.
type PartnerAnswer int

const (
	// PartnerAccepted is 202 processing, or 200 with the current status.
	PartnerAccepted PartnerAnswer = iota
	// PartnerRejected is a definitive refusal (422).
	PartnerRejected
	// PartnerUnknown is a timeout, a 5xx or a broken connection: the partner
	// may or may not have processed the transfer.
	PartnerUnknown
)

// PartnerSubmission is one PIX sent to the partner.
type PartnerSubmission struct {
	EndToEndID     transfer.EndToEndID
	AmountCents    money.Cents
	DestinationKey string
	CorrelationID  string
}

// PartnerSubmitter sends a PIX to the partner.
type PartnerSubmitter interface {
	Submit(ctx context.Context, submission PartnerSubmission) PartnerAnswer
}

// pendingOnBoot bounds the boot scan of the naive relay.
const pendingOnBoot = 500

// nextAttemptOf reads the attempt from E<id>-<attempt>; the fixed id means
// no attempt yet.
func nextAttemptOf(endToEndID transfer.EndToEndID) int {
	_, suffix, hasAttempt := strings.Cut(endToEndID.String(), "-")
	if !hasAttempt {
		return firstRelayAttempt
	}

	attempt, err := strconv.Atoi(suffix)
	if err != nil {
		return firstRelayAttempt
	}

	return attempt + 1
}

// postTransferStep writes one step of a transfer's saga in the ledger.
func postTransferStep(ctx context.Context, stores PostingStores, external ExternalTransfer, step transfer.Posting) (ledger.TransactionID, error) {
	kind, entries, err := stepEntries(external, step)
	if err != nil {
		return ledger.TransactionID{}, err
	}

	transactionID, err := ledger.NewTransactionID()
	if err != nil {
		return ledger.TransactionID{}, err
	}

	transferUUID := uuid.UUID(external.ID)
	transaction := ledger.Transaction{ID: transactionID, Kind: kind, CorrelationID: external.ClientRef, ExternalTransferID: &transferUUID}

	_, err = stores.InsertTransaction(ctx, transaction)
	if err != nil {
		return ledger.TransactionID{}, err
	}

	_, err = poster{positions: stores, entries: stores}.postAll(ctx, transactionID, entries)
	if err != nil {
		return ledger.TransactionID{}, err
	}

	return transactionID, nil
}

func stepEntries(external ExternalTransfer, step transfer.Posting) (ledger.TransactionKind, []posting, error) {
	amount := external.Amount.Cents()
	transit, settlement, customer := ledger.PixOutTransitAccountID(), ledger.PartnerSettlementAccountID(), external.AccountID

	switch step {
	case transfer.PostingSettle:
		return ledger.TransactionKindPixOutSettle, []posting{{transit, -amount}, {settlement, amount}}, nil
	case transfer.PostingRefund:
		return ledger.TransactionKindPixOutRefund, []posting{{transit, -amount}, {customer, amount}}, nil
	case transfer.PostingReturn:
		return ledger.TransactionKindPixOutReturn, []posting{{settlement, -amount}, {customer, amount}}, nil
	case transfer.PostingNone:
		return "", nil, fmt.Errorf("no ledger step for %s", step)
	default:
		return "", nil, fmt.Errorf("unknown posting %q", step)
	}
}
