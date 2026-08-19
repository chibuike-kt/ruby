package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/money"
)

var (
	ErrAmountRequired   = errors.New("ai: could not determine an amount")
	ErrCustomerRequired = errors.New("ai: could not determine which customer this is about")
)

// CustomerNotFoundError means a mutating-but-not-CREATE_DEBT intent (e.g.
// RECORD_PAYMENT, GET_CUSTOMER_BALANCE) named a customer who doesn't
// exist. Unlike CREATE_DEBT, these never auto-create — spec §37's
// "I couldn't determine..." style error is the correct response, not a
// guess.
type CustomerNotFoundError struct {
	Name string
}

func (e *CustomerNotFoundError) Error() string {
	return fmt.Sprintf("ai: no customer named %q", e.Name)
}

// ContextHint carries conversational signals the Validator can't derive
// from RawIntent alone. ResolvedCustomerID (spec §8 signal 3) takes
// priority over CustomerName entirely — it's Ruby's own answer to a
// disambiguation question it just asked, not something to re-resolve by
// name (which would just hit the same ambiguity again). LastCustomerID
// (spec §8 signal 4, pronoun resolution) is the fallback when RawIntent
// has no name at all.
type ContextHint struct {
	ResolvedCustomerID *int64
	LastCustomerID     *int64
}

// Validator turns untrusted RawIntent into an Action. It only ever reads
// (customer.Service.Resolve) — never customer.Service.Create,
// debt.Service.Create, or payment.Service.Record. Those happen in
// Processor.Execute, after any confidence/confirmation gate passes
// (decisions.md #5).
type Validator struct {
	Customers *customer.Service
}

func NewValidator(customers *customer.Service) *Validator {
	return &Validator{Customers: customers}
}

// Validate returns an Action, or an error — possibly a
// *customer.AmbiguousError, unmodified, so Processor can turn it into a
// pending disambiguation (see pending.go). CONFIRM_ACTION and
// CREATE_REMINDER/CANCEL_REMINDER are intercepted by Processor before
// reaching here (see processor.go); an intent Validate doesn't otherwise
// recognize falls back to UnsupportedAction rather than erroring, since a
// model producing an intent string outside the schema's enum should never
// be possible under strict Structured Outputs, but "should never happen"
// isn't "must crash."
func (v *Validator) Validate(ctx context.Context, userID int64, raw RawIntent, hint ContextHint) (Action, error) {
	switch raw.Intent {
	case IntentCreateDebt:
		return v.validateCreateDebt(ctx, userID, raw, hint)
	case IntentRecordPayment:
		return v.validateRecordPayment(ctx, userID, raw, hint)
	case IntentGetCustomerBalance:
		return v.validateGetCustomerBalance(ctx, userID, raw, hint)
	case IntentListCustomers:
		return ListCustomersAction{}, nil
	case IntentListOutstandingDebts:
		return ListOutstandingDebtsAction{}, nil
	case IntentGetTotalOutstanding:
		return GetTotalOutstandingAction{}, nil
	case IntentGetPaymentSummary:
		return GetPaymentSummaryAction{}, nil
	case IntentHelp:
		return HelpAction{}, nil
	default:
		return UnsupportedAction{Intent: raw.Intent}, nil
	}
}

func (v *Validator) validateCreateDebt(ctx context.Context, userID int64, raw RawIntent, hint ContextHint) (Action, error) {
	amount, err := parseAmount(raw.AmountMinor)
	if err != nil {
		return nil, err
	}
	ref, err := v.resolveOrNewCustomer(ctx, userID, raw.CustomerName, hint)
	if err != nil {
		return nil, err
	}
	return CreateDebtAction{
		Customer:    ref,
		Amount:      amount,
		Description: trimOrEmpty(raw.Description),
		DueDate:     parseDueDate(raw.DueDateISO),
	}, nil
}

func (v *Validator) validateRecordPayment(ctx context.Context, userID int64, raw RawIntent, hint ContextHint) (Action, error) {
	amount, err := parseAmount(raw.AmountMinor)
	if err != nil {
		return nil, err
	}
	customerID, err := v.resolveExistingCustomer(ctx, userID, raw.CustomerName, hint)
	if err != nil {
		return nil, err
	}
	return RecordPaymentAction{CustomerID: customerID, Amount: amount}, nil
}

func (v *Validator) validateGetCustomerBalance(ctx context.Context, userID int64, raw RawIntent, hint ContextHint) (Action, error) {
	customerID, err := v.resolveExistingCustomer(ctx, userID, raw.CustomerName, hint)
	if err != nil {
		return nil, err
	}
	return GetCustomerBalanceAction{CustomerID: customerID}, nil
}

// resolveExistingCustomer never creates — used by every intent except
// CREATE_DEBT.
func (v *Validator) resolveExistingCustomer(ctx context.Context, userID int64, name *string, hint ContextHint) (int64, error) {
	ref, err := customerRefFrom(name, hint)
	if err != nil {
		return 0, err
	}
	c, err := v.Customers.Resolve(ctx, userID, ref)
	if err != nil {
		if errors.Is(err, customer.ErrNotFound) {
			return 0, &CustomerNotFoundError{Name: trimOrEmpty(name)}
		}
		return 0, err // includes *customer.AmbiguousError
	}
	return c.ID, nil
}

// resolveOrNewCustomer is CREATE_DEBT's own resolution: a name that
// doesn't resolve to an existing customer becomes a CustomerRef.NewName,
// not an error — the actual customer.Service.Create call still waits
// until Processor.Execute (decisions.md #5, plan decision #1).
func (v *Validator) resolveOrNewCustomer(ctx context.Context, userID int64, name *string, hint ContextHint) (CustomerRef, error) {
	ref, err := customerRefFrom(name, hint)
	if err != nil {
		return CustomerRef{}, err
	}
	c, err := v.Customers.Resolve(ctx, userID, ref)
	switch {
	case err == nil:
		id := c.ID
		return CustomerRef{ExistingID: &id}, nil
	case errors.Is(err, customer.ErrNotFound):
		if ref.Name == nil {
			// The hint pointed at a customer that no longer exists —
			// surface it rather than silently fabricating a new one
			// under a name we were never actually given.
			return CustomerRef{}, err
		}
		n := *ref.Name
		return CustomerRef{NewName: &n}, nil
	default:
		return CustomerRef{}, err // includes *customer.AmbiguousError
	}
}

func customerRefFrom(name *string, hint ContextHint) (customer.Ref, error) {
	if hint.ResolvedCustomerID != nil {
		return customer.Ref{CustomerID: hint.ResolvedCustomerID}, nil
	}
	if n := trimOrEmpty(name); n != "" {
		return customer.Ref{Name: &n}, nil
	}
	if hint.LastCustomerID != nil {
		return customer.Ref{CustomerID: hint.LastCustomerID}, nil
	}
	return customer.Ref{}, ErrCustomerRequired
}

func parseAmount(minor *int64) (money.Money, error) {
	if minor == nil || *minor <= 0 {
		return money.Money{}, ErrAmountRequired
	}
	return money.New(*minor, money.NGN), nil
}

// parseDueDate never fails the whole intent over a malformed date: the
// amount and customer are the financially load-bearing fields, and a
// model that reports low confidence about a date already routes through
// the confirmation gate (Processor.Execute) — no extra per-field
// confidence logic needed here. An unparseable due_date_iso is simply
// dropped rather than blocking debt creation, and spec §21's "no due
// date" case (nil) is the same code path as "malformed due date."
func parseDueDate(iso *string) *time.Time {
	if iso == nil {
		return nil
	}
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(*iso))
	if err != nil {
		return nil
	}
	return &t
}

func trimOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
