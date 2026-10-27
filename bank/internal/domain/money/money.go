// Package money holds the value types of the bank: amounts are integer cents,
// never floats, in a single currency (BRL).
package money

import "errors"

// MaxOperationCents is the largest amount a single operation may move (an
// amount is positive and at most R$ 1,000,000.00: R$ 1.000.000,00).
const MaxOperationCents Cents = 100_000_000

var (
	ErrAmountNotPositive = errors.New("amount must be greater than zero")
	ErrAmountAboveLimit  = errors.New("amount is above the per-operation limit")
	ErrBalanceOverflow   = errors.New("balance overflow")
)

type Cents int64

type Amount struct {
	cents Cents
}

type Balance struct {
	cents Cents
}

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

func (a Amount) Cents() Cents {
	return a.cents
}

func (a Amount) Negated() Cents {
	return -a.cents
}

func NewBalance(cents Cents) Balance {
	return Balance{cents: cents}
}

func (b Balance) Cents() Cents {
	return b.cents
}

func (b Balance) Add(delta Cents) (Balance, error) {
	sum := b.cents + delta

	hasOverflowed := (delta > 0 && sum < b.cents) || (delta < 0 && sum > b.cents)
	if hasOverflowed {
		return Balance{}, ErrBalanceOverflow
	}

	return Balance{cents: sum}, nil
}

func (b Balance) Covers(amount Amount) bool {
	return b.cents >= amount.cents
}
