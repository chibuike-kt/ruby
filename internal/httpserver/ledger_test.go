package httpserver_test

import (
	"net/http"
	"testing"
)

func TestListLedger_ByUser(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348100000001")
	customerID := createTestCustomer(t, env, userID, "Ada")
	debtID := createTestDebt(t, env, userID, customerID, 4500000)
	env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID,
		map[string]any{"amount_minor": 2000000}, map[string]string{"Idempotency-Key": "key-1"})

	rec := env.do(t, http.MethodGet, "/api/ledger", userID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	entries := decodeBody[[]map[string]any](t, rec)
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (DEBT_CREATED, PAYMENT_RECORDED)", len(entries))
	}
}

func TestListLedger_ByDebt(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348100000002")
	customerID := createTestCustomer(t, env, userID, "Ada")
	debtID := createTestDebt(t, env, userID, customerID, 4500000)

	rec := env.do(t, http.MethodGet, "/api/ledger?debt_id="+itoa(debtID), userID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	entries := decodeBody[[]map[string]any](t, rec)
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1 (DEBT_CREATED)", len(entries))
	}
}

func TestListLedger_ByDebt_CrossUserDenied(t *testing.T) {
	env := newTestEnv(t)
	owner := env.newUser(t, "+2348100000003")
	other := env.newUser(t, "+2348100000004")
	customerID := createTestCustomer(t, env, owner, "Ada")
	debtID := createTestDebt(t, env, owner, customerID, 4500000)

	rec := env.do(t, http.MethodGet, "/api/ledger?debt_id="+itoa(debtID), other, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404 (spec §35 — ledger.ListByDebt isn't user-scoped, the handler must enforce it)", rec.Code)
	}
}

func TestListLedger_ByDebt_InvalidID(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348100000005")

	rec := env.do(t, http.MethodGet, "/api/ledger?debt_id=not-a-number", userID, nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", rec.Code)
	}
}
