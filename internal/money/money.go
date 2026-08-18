// Package money represents currency amounts as integer minor units.
// No float64 is used anywhere in this package. A Money value of 7500000
// in NGN means 75,000 naira, since 1 naira = 100 kobo.
package money

import (
	"errors"
	"fmt"
)

// ErrNegativeAmount is returned when an operation would produce a negative
// balance. Ruby never silently clamps to zero — the caller decides what
// a would-be-negative result means for their operation.
var ErrNegativeAmount = errors.New("money: amount cannot be negative")

// ErrCurrencyMismatch is returned when two Money values with different
// currencies are combined. Ruby does not do implicit currency conversion.
var ErrCurrencyMismatch = errors.New("money: currency mismatch")

// Currency is an ISO 4217 currency code, e.g. "NGN".
type Currency string

const (
	NGN Currency = "NGN"
	USD Currency = "USD"
)

// minorUnitsPerCurrency defines how many minor units make up one major
// unit, per currency. NGN: 1 naira = 100 kobo.
var minorUnitsPerCurrency = map[Currency]int64{
	NGN: 100,
	USD: 100,
}

// Money is an amount in minor units (e.g. kobo) of a given currency.
// The zero value is zero NGN, which is a valid, usable value.
type Money struct {
	minorUnits int64
	currency   Currency
}

// New constructs a Money value directly from minor units. Use this when
// you already have kobo, not naira.
func New(minorUnits int64, currency Currency) Money {
	return Money{minorUnits: minorUnits, currency: currency}
}

// NewFromMajor constructs a Money value from a major-unit integer amount
// (e.g. whole naira) with no fractional part. Fractional major-unit input
// must go through a caller-supplied minor-unit value instead — this
// package never parses or rounds decimal strings, since that is exactly
// the kind of float-adjacent operation the spec forbids for financial math.
func NewFromMajor(majorUnits int64, currency Currency) (Money, error) {
	factor, ok := minorUnitsPerCurrency[currency]
	if !ok {
		return Money{}, fmt.Errorf("money: unsupported currency %q", currency)
	}
	return Money{minorUnits: majorUnits * factor, currency: currency}, nil
}

// MinorUnits returns the raw minor-unit amount (e.g. kobo).
func (m Money) MinorUnits() int64 { return m.minorUnits }

// Currency returns the currency code.
func (m Money) Currency() Currency { return m.currency }

// IsZero reports whether the amount is zero.
func (m Money) IsZero() bool { return m.minorUnits == 0 }

// IsNegative reports whether the amount is negative. Money values can be
// negative as intermediate results (e.g. a delta) even though most
// domain balances should never end up negative.
func (m Money) IsNegative() bool { return m.minorUnits < 0 }

// Add returns m + other. Returns ErrCurrencyMismatch if the currencies differ.
func (m Money) Add(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	return Money{minorUnits: m.minorUnits + other.minorUnits, currency: m.currency}, nil
}

// Sub returns m - other. Returns ErrCurrencyMismatch if the currencies differ.
// It does not reject negative results — callers that need a non-negative
// invariant (e.g. outstanding balance) must check IsNegative explicitly.
// This keeps the type honest: Money itself does not decide domain rules
// about what a negative balance means.
func (m Money) Sub(other Money) (Money, error) {
	if m.currency != other.currency {
		return Money{}, ErrCurrencyMismatch
	}
	return Money{minorUnits: m.minorUnits - other.minorUnits, currency: m.currency}, nil
}

// GreaterThan reports whether m > other. Panics on currency mismatch,
// since comparing across currencies is a programming error, not a
// recoverable runtime condition.
func (m Money) GreaterThan(other Money) bool {
	if m.currency != other.currency {
		panic(fmt.Sprintf("money: cannot compare %s to %s", m.currency, other.currency))
	}
	return m.minorUnits > other.minorUnits
}

// String renders the amount in major units with two decimal places,
// e.g. "NGN 75000.00". This is for logging and debugging only — it is
// not a currency-formatting library and must never be parsed back.
func (m Money) String() string {
	factor := minorUnitsPerCurrency[m.currency]
	if factor == 0 {
		factor = 100
	}
	whole := m.minorUnits / factor
	frac := m.minorUnits % factor
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%s %d.%02d", m.currency, whole, frac)
}
