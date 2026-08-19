package ai_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

// TestRawIntent_HasNoIDShapedField is the structural half of spec §45's
// "invalid AI output" test: decisions.md #5 holds because RawIntent
// simply has no field that could carry a raw database id — the model
// can name a customer, never address one directly. If a future change
// ever adds an *_id/ID field to RawIntent, this test catches it.
func TestRawIntent_HasNoIDShapedField(t *testing.T) {
	for field := range reflect.TypeFor[ai.RawIntent]().Fields() {
		if field.Name == "ID" || (len(field.Name) >= 2 && field.Name[len(field.Name)-2:] == "ID") {
			t.Fatalf("RawIntent has an id-shaped field %q — the AI boundary (decisions.md #5) relies on this never being true", field.Name)
		}
	}
}

func TestValidate_NegativeAmount_NeverReachesDebtCreate(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348020000001")
	v := ai.NewValidator(customer.NewService(pool))

	negative := int64(-7500000)
	_, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentCreateDebt,
		CustomerName: new("Chinedu"),
		AmountMinor:  &negative,
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})
	if !errors.Is(err, ai.ErrAmountRequired) {
		t.Fatalf("got error %v, want ErrAmountRequired", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM debts WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("count debts: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d debt rows after a negative-amount intent, want 0 — it must never reach debt.Service.Create", count)
	}
}

func TestValidate_ZeroAmount_NeverReachesDebtCreate(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348020000002")
	v := ai.NewValidator(customer.NewService(pool))

	zero := int64(0)
	_, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentCreateDebt,
		CustomerName: new("Chinedu"),
		AmountMinor:  &zero,
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})
	if !errors.Is(err, ai.ErrAmountRequired) {
		t.Fatalf("got error %v, want ErrAmountRequired", err)
	}
}

func TestValidate_NegativeAmount_NeverReachesPaymentRecord(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348020000003")
	customers := customer.NewService(pool)
	if _, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	v := ai.NewValidator(customers)
	negative := int64(-1000000)
	_, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentRecordPayment,
		CustomerName: new("Chinedu"),
		AmountMinor:  &negative,
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})
	if !errors.Is(err, ai.ErrAmountRequired) {
		t.Fatalf("got error %v, want ErrAmountRequired", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM payments`).Scan(&count); err != nil {
		t.Fatalf("count payments: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d payment rows after a negative-amount intent, want 0 — it must never reach payment.Service.Record", count)
	}
}

// TestValidate_IntentMismatch_DoesNotCreateFromDescriptionAlone guards
// against a malformed/manipulated response where intent doesn't match
// what a service would need: HELP with debt-shaped fields attached must
// never be treated as CREATE_DEBT just because amount/customer_name
// happen to be populated.
func TestValidate_IntentMismatch_NoActionTaken(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348020000004")
	v := ai.NewValidator(customer.NewService(pool))

	action, err := v.Validate(context.Background(), userID, ai.RawIntent{
		Intent:       ai.IntentHelp,
		CustomerName: new("Chinedu"),
		AmountMinor:  new(int64(9999999999)),
		Confidence:   ai.ConfidenceHigh,
		Language:     ai.LangEnglish,
	}, ai.ContextHint{})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, ok := action.(ai.HelpAction); !ok {
		t.Fatalf("got action %#v, want HelpAction regardless of amount/customer_name being populated", action)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM debts WHERE user_id = $1`, userID).Scan(&count); err != nil {
		t.Fatalf("count debts: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d debt rows for a HELP intent, want 0", count)
	}
}
