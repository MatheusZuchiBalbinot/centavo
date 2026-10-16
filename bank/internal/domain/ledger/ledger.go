// Package ledger holds accounts, ledger transactions and entries, and the
// rules that decide whether money may move (docs/domain.md, sections 3 and 4).
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

// AccountID identifies an account.
type AccountID uuid.UUID

// TransactionID identifies a ledger transaction.
type TransactionID uuid.UUID

// OperationID identifies one client write as the bank executed it.
type OperationID uuid.UUID

// AccountKind tells customer accounts (floor at zero) from system accounts.
type AccountKind string

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
)

// TransactionKind is the kind of a ledger transaction.
type TransactionKind string

const (
	TransactionKindDeposit          TransactionKind = "deposit"
	TransactionKindInternalTransfer TransactionKind = "internal_transfer"
	TransactionKindPixOutReserve    TransactionKind = "pix_out_reserve"
	TransactionKindPixOutSettle     TransactionKind = "pix_out_settle"
	TransactionKindPixOutRefund     TransactionKind = "pix_out_refund"
	TransactionKindPixOutReturn     TransactionKind = "pix_out_return"
	TransactionKindPixInCredit      TransactionKind = "pix_in_credit"
)

// Seq is the position of an entry within its account: 1, 2, 3...
type Seq int64

const maxHolderNameLength = 120

var (
	ErrAccountNotFound    = errors.New("account not found")
	ErrSameAccount        = errors.New("a transfer needs two distinct accounts")
	ErrInsufficientFunds  = errors.New("account balance is lower than the requested amount")
	ErrInvalidHolderName  = errors.New("holder name must have between 1 and 120 characters")
	ErrInvalidAccountID   = errors.New("account id must be a UUID")
	errIDGenerationFailed = errors.New("generating id")
)

// Account is a customer or system account.
type Account struct {
	ID         AccountID
	Kind       AccountKind
	HolderName string
	CreatedAt  time.Time
}

// Position is where an account stands: its balance and the position of its
// last entry.
type Position struct {
	Balance money.Balance
	LastSeq Seq
}

// Transaction is the header of a ledger transaction; its entries sum to zero.
// Every transaction names its cause (ledger_tx_has_cause): the client
// operation, the external transfer or the incoming PIX behind it.
type Transaction struct {
	ID            TransactionID
	Kind          TransactionKind
	OperationID   *OperationID
	ClientRef     string
	CorrelationID string
	// ExternalTransferID is the PIX out the transaction belongs to.
	ExternalTransferID *uuid.UUID
	// IncomingTransferID is the PIX in the transaction credits.
	IncomingTransferID *uuid.UUID
}

// Entry is one immutable line that changes the balance of an account.
type Entry struct {
	TransactionID TransactionID
	AccountID     AccountID
	Amount        money.Cents
	Seq           Seq
	BalanceAfter  money.Balance
}

// StatementLine is an entry as the account holder sees it.
type StatementLine struct {
	Seq             Seq
	TransactionID   TransactionID
	TransactionKind TransactionKind
	Amount          money.Cents
	BalanceAfter    money.Balance
	CreatedAt       time.Time
}

// NewAccountID generates a time-ordered account id.
func NewAccountID() (AccountID, error) {
	id, err := newV7()

	return AccountID(id), err
}

// NewTransactionID generates a time-ordered transaction id.
func NewTransactionID() (TransactionID, error) {
	id, err := newV7()

	return TransactionID(id), err
}

// NewOperationID generates a time-ordered operation id.
func NewOperationID() (OperationID, error) {
	id, err := newV7()

	return OperationID(id), err
}

// ExternalCashAccountID is sys_external_cash, which mirrors money that came in
// by direct deposit (docs/domain.md, section 3.2).
func ExternalCashAccountID() AccountID {
	return externalCashAccountID
}

// PixOutTransitAccountID is sys_pix_out_transit: money reserved for PIX out
// that has not settled or come back yet.
func PixOutTransitAccountID() AccountID {
	return pixOutTransitAccountID
}

// PartnerSettlementAccountID is sys_partner_settlement: the mirror of the
// bank's reserve at the partner.
func PartnerSettlementAccountID() AccountID {
	return partnerSettlementAccountID
}

// ParseAccountID reads an account id from its canonical text form.
func ParseAccountID(raw string) (AccountID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return AccountID{}, ErrInvalidAccountID
	}

	return AccountID(id), nil
}

func (id AccountID) String() string     { return uuid.UUID(id).String() }
func (id TransactionID) String() string { return uuid.UUID(id).String() }
func (id OperationID) String() string   { return uuid.UUID(id).String() }

// ParseHolderName trims and validates the name of a customer.
func ParseHolderName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	length := utf8.RuneCountInString(name)

	isValid := length > 0 && length <= maxHolderNameLength && utf8.ValidString(name)
	if !isValid {
		return "", ErrInvalidHolderName
	}

	return name, nil
}

// CheckTransferAccounts enforces BR05: two distinct accounts.
func CheckTransferAccounts(from, to AccountID) error {
	if from == to {
		return ErrSameAccount
	}

	return nil
}

// Debit returns the position after paying amount, refusing to overdraw (BR01).
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
