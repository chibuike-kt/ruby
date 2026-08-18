package httpserver_test

import (
	"net/http"
	"testing"
)

func TestSummary(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348110000001")
	customerID := createTestCustomer(t, env, userID, "Ada")
	debtID := createTestDebt(t, env, userID, customerID, 12000000)
	env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID,
		map[string]any{"amount_minor": 5000000}, map[string]string{"Idempotency-Key": "key-1"})

	rec := env.do(t, http.MethodGet, "/api/summary", userID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	totals := decodeBody[[]map[string]any](t, rec)
	if len(totals) != 1 {
		t.Fatalf("got %d currency buckets, want 1", len(totals))
	}
	row := totals[0]
	if row["currency"] != "NGN" {
		t.Fatalf("got currency %v, want NGN", row["currency"])
	}
	if row["total_credit_issued_minor"] != float64(12000000) {
		t.Fatalf("got total_credit_issued_minor %v, want 12000000", row["total_credit_issued_minor"])
	}
	if row["total_collected_minor"] != float64(5000000) {
		t.Fatalf("got total_collected_minor %v, want 5000000", row["total_collected_minor"])
	}
	if row["total_outstanding_minor"] != float64(7000000) {
		t.Fatalf("got total_outstanding_minor %v, want 7000000", row["total_outstanding_minor"])
	}
}

func TestSummary_ScopedToUser(t *testing.T) {
	env := newTestEnv(t)
	userA := env.newUser(t, "+2348110000002")
	userB := env.newUser(t, "+2348110000003")
	custA := createTestCustomer(t, env, userA, "Ada")
	createTestDebt(t, env, userA, custA, 1000000)

	rec := env.do(t, http.MethodGet, "/api/summary", userB, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	totals := decodeBody[[]map[string]any](t, rec)
	if len(totals) != 0 {
		t.Fatalf("got %d currency buckets for an unrelated user, want 0", len(totals))
	}
}
