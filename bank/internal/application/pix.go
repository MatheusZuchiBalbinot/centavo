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
	"centavo/bank/internal/platform/correlation"
	"centavo/bank/internal/platform/failpoint"
)

// The PIX use cases of phase F6 are naive by design (docs/architecture.md,
// sections 5.4 and 5.5): the publication is a NOTIFY that is lost when no
// worker listens (FC04), the relay sends a new end_to_end_id on every
// attempt (FC05) and gives up on a timeout (FC21), and the webhook handler
// applies the effect directly, overwriting the status (FC24 to FC26). The
// robust variants arrive with defenses D4 to D9.

// Causes recorded in the transition history.
const (
	causeAPI          = "api"
	causeRelay        = "relay"
	causeWebhook      = "webhook"
	failurePartner    = "partner_rejected"
	failureTimeout    = "partner_timeout"
	firstRelayAttempt = 1
)

// Errors of the PIX use cases.
var (
	ErrUnknownEvent = errors.New("unknown partner event")
	// ErrAlreadyCredited means the incoming PIX was credited before (BR12).
	ErrAlreadyCredited = errors.New("incoming PIX already credited")
)

// ExternalTransfer is a PIX out as stored.
type ExternalTransfer struct {
	ID             transfer.ID
	EndToEndID     string
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
	Cause               string
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
	EndToEndID    string
	AccountID     ledger.AccountID
	Amount        money.Amount
	SourceEventID string
}

// PixStore keeps external transfers, their transitions and incoming PIX.
type PixStore interface {
	InsertExternalTransfer(ctx context.Context, external ExternalTransfer) (time.Time, error)
	InsertTransition(ctx context.Context, transition TransitionRecord) error
	FindExternalTransfer(ctx context.Context, id transfer.ID) (ExternalTransfer, []TransitionRecord, error)
	FindExternalTransferByEndToEnd(ctx context.Context, endToEndID string) (ExternalTransfer, error)
	// ClaimAttempt moves the transfer from one end_to_end_id to the next, so
	// only one relay sends each attempt.
	ClaimAttempt(ctx context.Context, id transfer.ID, seen, next string) (bool, error)
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
	LockEndToEnd(ctx context.Context, endToEndID string) error
	// LockTransfersOf locks the transfers of these end_to_end_ids, in id order.
	LockTransfersOf(ctx context.Context, endToEndIDs []string) error
	// ListCreated returns transfers still in created, oldest first.
	ListCreated(ctx context.Context, limit int) ([]transfer.ID, error)
}

// PixOutInput asks to send money out through the partner.
type PixOutInput struct {
	AccountID      ledger.AccountID
	Amount         money.Amount
	DestinationKey string
	Write          WriteContext
}

// PixOutResult is what the client gets back: the transfer starts as created.
type PixOutResult struct {
	TransferID transfer.ID
	EndToEndID string
	Status     transfer.Status
	CreatedAt  time.Time
}

// CreatePixOut reserves the amount and creates the transfer (BR08).
type CreatePixOut struct {
	locker      Locker
	publication Publication
}

// NewCreatePixOut builds the use case.
func NewCreatePixOut(locker Locker, publication Publication) CreatePixOut {
	return CreatePixOut{locker: locker, publication: publication}
}

// Apply runs inside the writer's unit of work: lock, check funds, move the
// amount to sys_pix_out_transit and record the transfer and its first state.
func (c CreatePixOut) Apply(ctx context.Context, stores Stores, operationID ledger.OperationID, input PixOutInput) (PixOutResult, error) {
	debited, err := c.reserveFunds(ctx, stores, input)
	if err != nil {
		return PixOutResult{}, err
	}

	external, createdAt, err := recordTransfer(ctx, stores, operationID, input)
	if err != nil {
		return PixOutResult{}, err
	}

	transactionID, err := postReserve(ctx, stores, reservation{external: external, debited: debited, write: input.Write})
	if err != nil {
		return PixOutResult{}, err
	}

	first := TransitionRecord{TransferID: external.ID, Seq: 1, To: transfer.StatusCreated, Cause: causeAPI, LedgerTransactionID: &transactionID, CorrelationID: input.Write.CorrelationID}

	err = stores.InsertTransition(ctx, first)
	if err != nil {
		return PixOutResult{}, err
	}

	err = c.publication.WithinCommit(ctx, stores, external, input.Write.CorrelationID)
	if err != nil {
		return PixOutResult{}, err
	}

	return PixOutResult{TransferID: external.ID, EndToEndID: external.EndToEndID, Status: transfer.StatusCreated, CreatedAt: createdAt}, nil
}

// reserveFunds locks the customer and transit and returns the customer's
// position after the debit, refusing to overdraw (BR01).
func (c CreatePixOut) reserveFunds(ctx context.Context, stores Stores, input PixOutInput) (ledger.Position, error) {
	// Transit is locked with the customer, in id order, so the reserve takes
	// it first, like a refund that moves transit then the customer.
	err := c.locker.Lock(ctx, stores, []ledger.AccountID{input.AccountID, ledger.PixOutTransitAccountID()})
	if err != nil {
		return ledger.Position{}, err
	}

	source, err := stores.ReadCustomerPosition(ctx, input.AccountID)
	if err != nil {
		return ledger.Position{}, err
	}

	return source.Debit(input.Amount)
}

// recordTransfer creates the external transfer in created.
func recordTransfer(ctx context.Context, stores Stores, operationID ledger.OperationID, input PixOutInput) (ExternalTransfer, time.Time, error) {
	id, err := transfer.NewID()
	if err != nil {
		return ExternalTransfer{}, time.Time{}, err
	}

	external := ExternalTransfer{ID: id, EndToEndID: id.EndToEndID(), OperationID: operationID, ClientRef: string(input.Write.Key), AccountID: input.AccountID, Amount: input.Amount, DestinationKey: input.DestinationKey, Status: transfer.StatusCreated, StatusSeq: 1}

	createdAt, err := stores.InsertExternalTransfer(ctx, external)
	if err != nil {
		return ExternalTransfer{}, time.Time{}, err
	}

	return external, createdAt, nil
}

// reservation is a PIX out whose funds are checked, ready to move to transit.
type reservation struct {
	external ExternalTransfer
	debited  ledger.Position
	write    WriteContext
}

// postReserve writes the reserve: the customer's debit and its twin in
// sys_pix_out_transit, in one ledger transaction tied to the transfer.
func postReserve(ctx context.Context, stores Stores, r reservation) (ledger.TransactionID, error) {
	transaction, err := newTransaction(ledger.TransactionKindPixOutReserve, r.write, r.external.OperationID)
	if err != nil {
		return ledger.TransactionID{}, err
	}

	transferUUID := uuid.UUID(r.external.ID)
	transaction.ExternalTransferID = &transferUUID

	_, err = stores.InsertTransaction(ctx, transaction)
	if err != nil {
		return ledger.TransactionID{}, err
	}

	entries := poster{positions: stores, entries: stores}

	line := debitLine{account: r.external.AccountID, debited: r.debited, amount: r.external.Amount}

	err = entries.debit(ctx, transaction.ID, line)
	if err != nil {
		return ledger.TransactionID{}, err
	}

	_, err = entries.post(ctx, transaction.ID, ledger.PixOutTransitAccountID(), r.external.Amount.Cents())
	if err != nil {
		return ledger.TransactionID{}, err
	}

	return transaction.ID, nil
}

// Publisher wakes a relay for a new transfer.
type Publisher interface {
	Publish(ctx context.Context, id transfer.ID) error
}

// Publication is defense D4's strategy: how a new transfer reaches a relay.
type Publication interface {
	// WithinCommit runs in the unit of work that creates the transfer.
	WithinCommit(ctx context.Context, stores Stores, external ExternalTransfer, correlationID string) error
	// AfterCommit runs once the transfer is committed.
	AfterCommit(ctx context.Context, id transfer.ID) error
}

// NotifyAfterCommit is the naive publication: once the transfer is
// committed, a NOTIFY tells the workers. Dying between the two, or having
// no worker listening, leaves the transfer in created forever (FC04).
type NotifyAfterCommit struct {
	publisher Publisher
}

// NewNotifyAfterCommit builds the naive publication.
func NewNotifyAfterCommit(publisher Publisher) NotifyAfterCommit {
	return NotifyAfterCommit{publisher: publisher}
}

// WithinCommit does nothing: the naive bank publishes after the commit.
func (NotifyAfterCommit) WithinCommit(context.Context, Stores, ExternalTransfer, string) error {
	return nil
}

// AfterCommit sends the NOTIFY.
func (n NotifyAfterCommit) AfterCommit(ctx context.Context, id transfer.ID) error {
	return n.publisher.Publish(ctx, id)
}

// PublishAfterCommit runs the step after the commit, with FP04 before it.
// With the outbox (D4) the step does nothing and the failpoint is harmless.
func PublishAfterCommit(ctx context.Context, publication Publication, id transfer.ID) error {
	err := failpoint.Hit(ctx, failpoint.PixOutAfterSaveBeforePublish)
	if err != nil {
		return err
	}

	return publication.AfterCommit(ctx, id)
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
	EndToEndID     string
	AmountCents    money.Cents
	DestinationKey string
	CorrelationID  string
}

// PartnerSubmitter sends a PIX to the partner.
type PartnerSubmitter interface {
	Submit(ctx context.Context, submission PartnerSubmission) PartnerAnswer
}

// NaiveRelay sends transfers to the partner (docs/architecture.md, section 5.4).
type NaiveRelay struct {
	unitOfWork UnitOfWork
	partner    PartnerSubmitter
	machine    StateMachine
}

// NewNaiveRelay builds the relay.
func NewNaiveRelay(unitOfWork UnitOfWork, partner PartnerSubmitter, machine StateMachine) NaiveRelay {
	return NaiveRelay{unitOfWork: unitOfWork, partner: partner, machine: machine}
}

// Relay sends one transfer. Each attempt gets a new end_to_end_id, so a
// retry the partner already processed is paid twice (FC05); a timeout is
// taken as a failure and refunded, though the partner may have paid (FC21).
func (r NaiveRelay) Relay(ctx context.Context, id transfer.ID) error {
	var external ExternalTransfer

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		external, _, err = stores.FindExternalTransfer(ctx, id)

		return err
	})
	if err != nil {
		return err
	}

	if external.Status != transfer.StatusCreated {
		return nil
	}

	// The transfer's client_ref follows it into the logs and the failpoint
	// events, as the request's correlation id did in the API.
	ctx = correlation.With(ctx, external.ClientRef)

	attempt := nextAttemptOf(external.EndToEndID)
	attemptID := id.AttemptEndToEndID(attempt)

	isClaimed, err := r.claim(ctx, id, external.EndToEndID, attemptID)
	if err != nil || !isClaimed {
		return err
	}

	submission := PartnerSubmission{EndToEndID: attemptID, AmountCents: external.Amount.Cents(), DestinationKey: external.DestinationKey, CorrelationID: external.ClientRef}
	answer := r.partner.Submit(ctx, submission)

	return r.record(ctx, external, answer)
}

// pendingOnBoot bounds the boot scan of the naive relay.
const pendingOnBoot = 500

// RelayPending is the naive recovery: when a worker starts, it sends again
// every transfer still in created, with no lease. A transfer the dead worker
// had already handed to the partner goes out a second time, under a new
// end_to_end_id (FC05, PM-005).
func (r NaiveRelay) RelayPending(ctx context.Context) error {
	var pending []transfer.ID

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		pending, err = stores.ListCreated(ctx, pendingOnBoot)

		return err
	})
	if err != nil {
		return err
	}

	for _, id := range pending {
		err := r.Relay(ctx, id)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r NaiveRelay) claim(ctx context.Context, id transfer.ID, seen, next string) (bool, error) {
	var isClaimed bool

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		var err error

		isClaimed, err = stores.ClaimAttempt(ctx, id, seen, next)

		return err
	})

	return isClaimed, err
}

func (r NaiveRelay) record(ctx context.Context, external ExternalTransfer, answer PartnerAnswer) error {
	if answer == PartnerAccepted {
		// FP03: the partner took the PIX; dying here leaves it created and a
		// later attempt sends it again under a new id (FC05).
		err := failpoint.Hit(ctx, failpoint.RelayAfterPartnerCallBeforeSave)
		if err != nil {
			return err
		}
	}

	command := answerCommand(external, answer)

	err := r.unitOfWork.Run(ctx, func(ctx context.Context, stores Stores) error {
		return r.machine.Move(ctx, stores, command)
	})

	return ignoreMovedOn(err)
}

// nextAttemptOf reads the attempt from E<id>-<attempt>; the fixed id means
// no attempt yet.
func nextAttemptOf(endToEndID string) int {
	_, suffix, hasAttempt := strings.Cut(endToEndID, "-")
	if !hasAttempt {
		return firstRelayAttempt
	}

	attempt, err := strconv.Atoi(suffix)
	if err != nil {
		return firstRelayAttempt
	}

	return attempt + 1
}

// postTransferStep writes one step of a transfer's saga in the ledger
// (docs/domain.md, section 4.1).
func postTransferStep(ctx context.Context, stores Stores, external ExternalTransfer, step transfer.Posting) (ledger.TransactionID, error) {
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
