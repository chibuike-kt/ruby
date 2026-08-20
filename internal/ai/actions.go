package ai

import (
	"time"

	"github.com/chibuike-kt/ruby/internal/money"
)

// Action is what Validator.Validate produces: a description of what
// should happen, never the mutation itself. Only Processor.Execute turns
// one into real service calls, and only after any confidence/confirmation
// gate has passed.
type Action interface {
	isAction()
}

// CustomerRef identifies the customer a mutating Action targets. Exactly
// one of ExistingID or NewName is set. NewName defers the actual
// customer.Service.Create call to execution time (see plan decision #1)
// — Validate only ever reads, so a low-confidence/misheard name can't
// create a garbage customer row before the trader has confirmed anything.
type CustomerRef struct {
	ExistingID *int64
	NewName    *string
}

type CreateDebtAction struct {
	Customer    CustomerRef
	Amount      money.Money
	Description string
	DueDate     *time.Time
}

type RecordPaymentAction struct {
	CustomerID int64
	Amount     money.Money
}

type GetCustomerBalanceAction struct {
	CustomerID int64
}

// GetCustomerStatementAction backs docs/BRIEF-disambiguation-reminders-
// statements.md Tier 3's real per-customer account history.
type GetCustomerStatementAction struct {
	CustomerID int64
}

// CreateReminderAction and CancelReminderAction back docs/BRIEF-
// disambiguation-reminders-statements.md Tier 2's standalone reminder
// intents — CustomerID only, never a name: identity resolution already
// happened in Validate, same as every other mutating action here.
type CreateReminderAction struct {
	CustomerID   int64
	ReminderDate time.Time
}

type CancelReminderAction struct {
	CustomerID int64
}

type ListCustomersAction struct{}
type ListOutstandingDebtsAction struct{}
type GetTotalOutstandingAction struct{}
type GetPaymentSummaryAction struct{}
type HelpAction struct{}

// UnsupportedAction covers intents that are recognized but not yet
// implemented (CREATE_REMINDER/CANCEL_REMINDER — internal/reminder
// doesn't exist yet). Processor short-circuits to this before the
// Validator even runs; see processor.go.
type UnsupportedAction struct {
	Intent IntentType
}

func (CreateDebtAction) isAction()           {}
func (RecordPaymentAction) isAction()        {}
func (CreateReminderAction) isAction()       {}
func (CancelReminderAction) isAction()       {}
func (GetCustomerBalanceAction) isAction()   {}
func (GetCustomerStatementAction) isAction() {}
func (ListCustomersAction) isAction()        {}
func (ListOutstandingDebtsAction) isAction() {}
func (GetTotalOutstandingAction) isAction()  {}
func (GetPaymentSummaryAction) isAction()    {}
func (HelpAction) isAction()                 {}
func (UnsupportedAction) isAction()          {}
