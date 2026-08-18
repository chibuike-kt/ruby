package httpserver_test

import (
	"net/http"
	"testing"
)

func createTestDebt(t *testing.T, env testEnv, userID, customerID, amountMinor int64) int64 {
	t.Helper()
	rec := env.do(t, http.MethodPost, "/api/debts", userID, map[string]any{
		"customer_id":  customerID,
		"amount_minor": amountMinor,
	}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup debt: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody[map[string]any](t, rec)
	return int64(body["id"].(float64))
}

func TestRecordPayment(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000001")
	customerID := createTestCustomer(t, env, userID, "Chinedu")
	debtID := createTestDebt(t, env, userID, customerID, 7500000)

	rec := env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID,
		map[string]any{"amount_minor": 3000000},
		map[string]string{"Idempotency-Key": "key-1"},
	)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody[map[string]any](t, rec)
	if body["debt_status"] != "PARTIALLY_PAID" {
		t.Fatalf("got debt_status %v, want PARTIALLY_PAID", body["debt_status"])
	}
}

func TestRecordPayment_MissingIdempotencyKey(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000002")
	customerID := createTestCustomer(t, env, userID, "Chinedu")
	debtID := createTestDebt(t, env, userID, customerID, 7500000)

	rec := env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID,
		map[string]any{"amount_minor": 3000000}, nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", rec.Code)
	}
}

func TestRecordPayment_Overpayment(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000003")
	customerID := createTestCustomer(t, env, userID, "Chinedu")
	debtID := createTestDebt(t, env, userID, customerID, 7500000)

	rec := env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID,
		map[string]any{"amount_minor": 10000000},
		map[string]string{"Idempotency-Key": "key-over"},
	)

	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409 (spec §14)", rec.Code)
	}
	body := decodeBody[map[string]any](t, rec)
	if body["outstanding_minor"] != float64(7500000) {
		t.Fatalf("got outstanding_minor %v, want 7500000", body["outstanding_minor"])
	}
}

func TestRecordPayment_DuplicateIdempotencyKey_Replayed(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000004")
	customerID := createTestCustomer(t, env, userID, "Chinedu")
	debtID := createTestDebt(t, env, userID, customerID, 7500000)

	headers := map[string]string{"Idempotency-Key": "shared-key"}
	first := env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID,
		map[string]any{"amount_minor": 3000000}, headers)
	if first.Code != http.StatusCreated {
		t.Fatalf("first payment: got status %d, body=%s", first.Code, first.Body.String())
	}

	second := env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID,
		map[string]any{"amount_minor": 3000000}, headers)
	if second.Code != http.StatusOK {
		t.Fatalf("replayed payment: got status %d, want 200, body=%s", second.Code, second.Body.String())
	}

	firstBody := decodeBody[map[string]any](t, first)
	secondBody := decodeBody[map[string]any](t, second)
	if firstBody["id"] != secondBody["id"] {
		t.Fatalf("replay returned a different payment id: %v vs %v", firstBody["id"], secondBody["id"])
	}
}

func TestRecordPayment_AgainstSettledDebt(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000005")
	customerID := createTestCustomer(t, env, userID, "Chinedu")
	debtID := createTestDebt(t, env, userID, customerID, 5000000)

	settle := env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID,
		map[string]any{"amount_minor": 5000000},
		map[string]string{"Idempotency-Key": "key-settle"},
	)
	if settle.Code != http.StatusCreated {
		t.Fatalf("setup: got status %d, body=%s", settle.Code, settle.Body.String())
	}

	rec := env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID,
		map[string]any{"amount_minor": 1000000},
		map[string]string{"Idempotency-Key": "key-after-settle"},
	)
	if rec.Code != http.StatusConflict {
		t.Fatalf("got status %d, want 409", rec.Code)
	}
}
