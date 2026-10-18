// Package money holds the value types of the bank: amounts are integer cents,
// never floats, in a single currency (BRL).
package money

import "errors"

// Cents is the smallest unit of money.
type Cents int64

// MaxOperationCents is the largest amount a single operation may move
// (BR04: R$ 1.000.000,00).
const MaxOperationCents Cents = 100_000_000

// Errors of validating an amount.
var (
	ErrAmountNotPositive = errors.New("amount must be greater than zero")
	ErrAmountAboveLimit  = errors.New("amount is above the per-operation limit")
	ErrBalanceOverflow   = errors.New("balance overflow")
)

// Amount is the value of one operation: always positive and at most
// MaxOperationCents.
type Amount struct {
	cents Cents
}

// NewAmount validates an operation value (BR04).
func NewAmount(cents Cents) (Amount, error) {
	isPositive := cents > 0
	if !isPositive {
		return Amount{}, ErrAmountNotPositive
	}

	isWithinLimit := cents <= MaxOperationCents
	if !isWithinLimit {
		return Amount{}, ErrAmountAboveLimit
	}

	return Amount{cents: cents}, nil
}

// Cents returns the amount as a positive number of cents.
func (a Amount) Cents() Cents {
	return a.cents
}

// Negated returns the amount as a debit: a negative number of cents.
func (a Amount) Negated() Cents {
	return -a.cents
}

// Balance is a signed account balance.
type Balance struct {
	cents Cents
}

// NewBalance wraps a balance read from storage.
func NewBalance(cents Cents) Balance {
	return Balance{cents: cents}
}

// Cents returns the balance as a signed number of cents.
func (b Balance) Cents() Cents {
	return b.cents
}

// Add returns the balance moved by delta, refusing to wrap around.
func (b Balance) Add(delta Cents) (Balance, error) {
	sum := b.cents + delta

	hasOverflowed := (delta > 0 && sum < b.cents) || (delta < 0 && sum > b.cents)
	if hasOverflowed {
		return Balance{}, ErrBalanceOverflow
	}

	return Balance{cents: sum}, nil
}

// Covers reports whether the balance is enough to pay the amount.
func (b Balance) Covers(amount Amount) bool {
	return b.cents >= amount.cents
}
