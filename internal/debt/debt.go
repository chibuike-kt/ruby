// Package debt owns debt records and their state machine (spec §12,
// §38). Debt creation and its ledger entry are written atomically —
// see Service.Create — but the row-level locking that protects payments
// against concurrent overpayment (spec §33/§34) lives in the payment
// package, since it's the payment transaction that needs the lock.
package debt

import (
	"time"

	"github.com/chibuike-kt/ruby/internal/money"
)

type Status string

const (
	StatusOutstanding   Status = "OUTSTANDING"
	StatusPartiallyPaid Status = "PARTIALLY_PAID"
	StatusSettled       Status = "SETTLED"
)

// forwardTransitions encodes the one-way state machine from spec §38:
// OUTSTANDING can move to PARTIALLY_PAID or straight to SETTLED (a
// single payment can cover the whole debt); PARTIALLY_PAID can only
// move to SETTLED; SETTLED is terminal. There is no path back to an
// earlier state — that requires an explicit correction/refund workflow,
// which is out of scope for Slice 1.
var forwardTransitions = map[Status]map[Status]bool{
	StatusOutstanding:   {StatusPartiallyPaid: true, StatusSettled: true},
	StatusPartiallyPaid: {StatusSettled: true},
	StatusSettled:       {},
}

// CanTransitionTo reports whether moving from s to target is a valid
// forward transition, or a no-op staying in the same state.
func (s Status) CanTransitionTo(target Status) bool {
	if s == target {
		return true
	}
	return forwardTransitions[s][target]
}

type Debt struct {
	ID          int64
	UserID      int64
	CustomerID  int64
	Amount      money.Money
	Description string
	DueDate     *time.Time
	Status      Status
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
