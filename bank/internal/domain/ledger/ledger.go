package ledger

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"centavo/bank/internal/domain/money"
)

const (
	AccountKindCustomer AccountKind = "customer"
	AccountKindSystem   AccountKind = "system"
)

// The system accounts are seeded by the base migration 00002_ledger.sql;
// their ids are fixed data, so the bank never has to look them up.
var (
	externalCashAccountID      = AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000001"))
	pixOutTransitAccountID     = AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000002"))
	partnerSettlementAccountID = AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000003"))
	suspenseAccountID          = AccountID(uuid.MustParse("00000000-0000-7000-8000-000000000004"))
)

const (
	TransactionKindDeposit          TransactionKind = "deposit"
	TransactionKindInternalTransfer TransactionKind = "internal_transfer"
	TransactionKindPixOutReserve    TransactionKind = "pix_out_reserve"
	TransactionKindPixOutSettle     TransactionKind = "pix_out_settle"
	TransactionKindPixOutRefund     TransactionKind = "pix_out_refund"
	TransactionKindPixOutReturn     TransactionKind = "pix_out_return"
	TransactionKindPixInCredit      TransactionKind = "pix_in_credit"
	TransactionKindAdjustment       TransactionKind = "reconciliation_adjustment"
)

const maxHolderNameLength = 120

var (
	ErrAccountNotFound    = errors.New("account not found")
	ErrSameAccount        = errors.New("a transfer needs two distinct accounts")
	ErrInsufficientFunds  = errors.New("account balance is lower than the requested amount")
	ErrInvalidHolderName  = errors.New("holder name must have between 1 and 120 characters")
	ErrInvalidAccountID   = errors.New("account id must be a UUID")
	errIDGenerationFailed = errors.New("generating id")
)

type AccountID uuid.UUID

type TransactionID uuid.UUID

type OperationID uuid.UUID

type AccountKind string

type TransactionKind string

type Seq int64

type Account struct {
	ID         AccountID
	Kind       AccountKind
	HolderName string
	CreatedAt  time.Time
}

type Position struct {
	Balance money.Balance
	LastSeq Seq
}

// Its entries sum to zero, and every transaction names its cause
// (ledger_tx_has_cause): the client operation, the external transfer or the
// incoming PIX behind it.
type Transaction struct {
	ID                   TransactionID
	Kind                 TransactionKind
	OperationID          *OperationID
	ClientRef            string
	CorrelationID        string
	ExternalTransferID   *uuid.UUID
	IncomingTransferID   *uuid.UUID
	ReconciliationItemID *int64
}

type Entry struct {
	TransactionID TransactionID
	AccountID     AccountID
	Amount        money.Cents
	Seq           Seq
	BalanceAfter  money.Balance
}

type StatementLine struct {
	Seq             Seq
	TransactionID   TransactionID
	TransactionKind TransactionKind
	Amount          money.Cents
	BalanceAfter    money.Balance
	CreatedAt       time.Time
}

func NewAccountID() (AccountID, error) {
	id, err := newV7()

	return AccountID(id), err
}

func NewTransactionID() (TransactionID, error) {
	id, err := newV7()

	return TransactionID(id), err
}

func NewOperationID() (OperationID, error) {
	id, err := newV7()

	return OperationID(id), err
}

// Mirrors money that came in by direct deposit.
func ExternalCashAccountID() AccountID {
	return externalCashAccountID
}

// Money reserved for PIX out that has not settled or come back yet.
func PixOutTransitAccountID() AccountID {
	return pixOutTransitAccountID
}

// The mirror of the bank's reserve at the partner.
func PartnerSettlementAccountID() AccountID {
	return partnerSettlementAccountID
}

// Divergences with the partner that are explained and not yet settled.
func SuspenseAccountID() AccountID {
	return suspenseAccountID
}

func ParseAccountID(raw string) (AccountID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return AccountID{}, ErrInvalidAccountID
	}

	return AccountID(id), nil
}

func (id AccountID) String() string { return uuid.UUID(id).String() }

func (id TransactionID) String() string { return uuid.UUID(id).String() }

func (id OperationID) String() string { return uuid.UUID(id).String() }

func ParseHolderName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	length := utf8.RuneCountInString(name)

	isValid := length > 0 && length <= maxHolderNameLength && utf8.ValidString(name)
	if !isValid {
		return "", ErrInvalidHolderName
	}

	return name, nil
}

func CheckTransferAccounts(from, to AccountID) error {
	if from == to {
		return ErrSameAccount
	}

	return nil
}

func (p Position) Debit(amount money.Amount) (Position, error) {
	if !p.Balance.Covers(amount) {
		return Position{}, ErrInsufficientFunds
	}

	balance, err := p.Balance.Add(amount.Negated())
	if err != nil {
		return Position{}, err
	}

	return Position{Balance: balance, LastSeq: p.LastSeq + 1}, nil
}

func newV7() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("%w: %w", errIDGenerationFailed, err)
	}

	return id, nil
}
