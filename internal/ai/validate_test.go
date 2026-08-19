package ai_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

func TestValidate_CreateDebt_NewCustomer(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000001")
	v := ai.NewValidator(customer.NewService(pool))

	action, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentCreateDebt,
		CustomerName: new("Chinedu"),
		AmountMinor:  new(int64(7500000)),
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	debtAction, ok := action.(ai.CreateDebtAction)
	if !ok {
		t.Fatalf("got action type %T, want CreateDebtAction", action)
	}
	if debtAction.Customer.NewName == nil || *debtAction.Customer.NewName != "Chinedu" {
		t.Fatalf("got customer ref %+v, want NewName=Chinedu (customer doesn't exist yet)", debtAction.Customer)
	}
	if debtAction.Customer.ExistingID != nil {
		t.Fatalf("got ExistingID set for a customer that was never created")
	}
	if debtAction.Amount.MinorUnits() != 7500000 {
		t.Fatalf("got amount %d, want 7500000", debtAction.Amount.MinorUnits())
	}
}

func TestValidate_CreateDebt_ExistingCustomer(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000002")
	customers := customer.NewService(pool)
	existing, err := customers.Create(context.Background(), userID, "Ngozi", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	v := ai.NewValidator(customers)
	action, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentCreateDebt,
		CustomerName: new("Ngozi"),
		AmountMinor:  new(int64(1000000)),
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	debtAction := action.(ai.CreateDebtAction)
	if debtAction.Customer.ExistingID == nil || *debtAction.Customer.ExistingID != existing.ID {
		t.Fatalf("got customer ref %+v, want ExistingID=%d", debtAction.Customer, existing.ID)
	}
	if debtAction.Customer.NewName != nil {
		t.Fatalf("got NewName set for a customer that already exists")
	}
}

func TestValidate_CreateDebt_AmbiguousCustomer(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000003")
	customers := customer.NewService(pool)
	if _, err := customers.Create(context.Background(), userID, "Chinedu", new("+2348030000001"), nil); err != nil {
		t.Fatalf("seed customer 1: %v", err)
	}
	if _, err := customers.Create(context.Background(), userID, "Chinedu", new("+2348030000002"), nil); err != nil {
		t.Fatalf("seed customer 2: %v", err)
	}

	v := ai.NewValidator(customers)
	_, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentCreateDebt,
		CustomerName: new("Chinedu"),
		AmountMinor:  new(int64(1000000)),
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})

	var ambiguous *customer.AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got error %v, want *customer.AmbiguousError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(ambiguous.Candidates))
	}
}

func TestValidate_CreateDebt_MissingAmount(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000004")
	v := ai.NewValidator(customer.NewService(pool))

	_, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentCreateDebt,
		CustomerName: new("Chinedu"),
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})
	if !errors.Is(err, ai.ErrAmountRequired) {
		t.Fatalf("got error %v, want ErrAmountRequired", err)
	}
}

func TestValidate_CreateDebt_MissingCustomerNoHint(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000005")
	v := ai.NewValidator(customer.NewService(pool))

	_, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:      ai.IntentCreateDebt,
		AmountMinor: new(int64(1000000)),
		Confidence:  ai.ConfidenceHigh,
		Language:    ai.LangEnglish,
	}, ai.ContextHint{})
	if !errors.Is(err, ai.ErrCustomerRequired) {
		t.Fatalf("got error %v, want ErrCustomerRequired", err)
	}
}

func TestValidate_CreateDebt_ContextHintFallback(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000006")
	customers := customer.NewService(pool)
	existing, err := customers.Create(context.Background(), userID, "Ngozi", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	v := ai.NewValidator(customers)
	action, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:      ai.IntentRecordPayment, // "he paid me 30k" — no name extracted
		AmountMinor: new(int64(3000000)),
		Confidence:  ai.ConfidenceHigh,
		Language:    ai.LangEnglish,
	}, ai.ContextHint{LastCustomerID: &existing.ID})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	paymentAction := action.(ai.RecordPaymentAction)
	if paymentAction.CustomerID != existing.ID {
		t.Fatalf("got customer id %d, want %d (from context hint)", paymentAction.CustomerID, existing.ID)
	}
}

func TestValidate_CreateDebt_MalformedDueDateIgnored(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000007")
	v := ai.NewValidator(customer.NewService(pool))

	action, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentCreateDebt,
		CustomerName: new("Chinedu"),
		AmountMinor:  new(int64(1000000)),
		DueDateISO:   new("next week sometime"), // spec §21: ambiguous date, not fatal
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if action.(ai.CreateDebtAction).DueDate != nil {
		t.Fatalf("got a due date parsed from an unparseable string, want nil")
	}
}

func TestValidate_RecordPayment_CustomerNotFound(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000008")
	v := ai.NewValidator(customer.NewService(pool))

	_, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentRecordPayment,
		CustomerName: new("NoSuchTrader"),
		AmountMinor:  new(int64(1000000)),
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})

	var notFound *ai.CustomerNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("got error %v, want *ai.CustomerNotFoundError", err)
	}
	if notFound.Name != "NoSuchTrader" {
		t.Fatalf("got name %q, want NoSuchTrader", notFound.Name)
	}
}

func TestValidate_ReadOnlyIntents(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000009")
	v := ai.NewValidator(customer.NewService(pool))

	cases := []struct {
		intent ai.IntentType
		want   ai.Action
	}{
		{ai.IntentListCustomers, ai.ListCustomersAction{}},
		{ai.IntentListOutstandingDebts, ai.ListOutstandingDebtsAction{}},
		{ai.IntentGetTotalOutstanding, ai.GetTotalOutstandingAction{}},
		{ai.IntentGetPaymentSummary, ai.GetPaymentSummaryAction{}},
		{ai.IntentHelp, ai.HelpAction{}},
	}
	for _, tc := range cases {
		action, err := v.Validate(context.Background(), userID, ai.RawIntent{Intent: tc.intent, Language: ai.LangEnglish}, ai.ContextHint{})
		if err != nil {
			t.Fatalf("%s: Validate: %v", tc.intent, err)
		}
		if action != tc.want {
			t.Fatalf("%s: got %#v, want %#v", tc.intent, action, tc.want)
		}
	}
}
