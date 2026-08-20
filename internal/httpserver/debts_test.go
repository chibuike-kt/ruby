package httpserver_test

import (
	"net/http"
	"testing"
)

func createTestCustomer(t *testing.T, env testEnv, userID int64, name string) int64 {
	t.Helper()
	rec := env.do(t, http.MethodPost, "/api/customers", userID, map[string]any{"name": name}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup customer: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody[map[string]any](t, rec)
	return int64(body["id"].(float64))
}

func TestCreateDebt(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348080000001")
	customerID := createTestCustomer(t, env, userID, "Ngozi")

	rec := env.do(t, http.MethodPost, "/api/debts", userID, map[string]any{
		"customer_id":  customerID,
		"amount_minor": 12000000,
		"description":  "5 bags of rice",
	}, nil)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody[map[string]any](t, rec)
	if body["status"] != "OUTSTANDING" {
		t.Fatalf("got status field %v, want OUTSTANDING", body["status"])
	}
	if body["amount_minor"] != float64(12000000) {
		t.Fatalf("got amount_minor %v, want 12000000", body["amount_minor"])
	}
}

// TestCreateDebt_NoDueDate_Succeeds is docs/BRIEF-critical-fixes-and-
// reminders.md #1a's REST-path counterpart to TestCreateDebt (which
// already omits due_date) — explicit by name so the requirement is
// asserted, not just incidentally exercised.
func TestCreateDebt_NoDueDate_Succeeds(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348080000099")
	customerID := createTestCustomer(t, env, userID, "Chinedu")

	rec := env.do(t, http.MethodPost, "/api/debts", userID, map[string]any{
		"customer_id":  customerID,
		"amount_minor": 5000000,
	}, nil)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201 — a missing due_date must not fail, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody[map[string]any](t, rec)
	if v, ok := body["due_date"]; ok && v != nil {
		t.Fatalf("got due_date %v, want absent/null — never invent one", v)
	}
}

func TestCreateDebt_WithDueDate(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348080000002")
	customerID := createTestCustomer(t, env, userID, "Chinedu")

	rec := env.do(t, http.MethodPost, "/api/debts", userID, map[string]any{
		"customer_id":  customerID,
		"amount_minor": 7500000,
		"due_date":     "2026-08-28",
	}, nil)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
}

func TestCreateDebt_InvalidDueDate(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348080000003")
	customerID := createTestCustomer(t, env, userID, "Chinedu")

	rec := env.do(t, http.MethodPost, "/api/debts", userID, map[string]any{
		"customer_id":  customerID,
		"amount_minor": 7500000,
		"due_date":     "next Friday",
	}, nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", rec.Code)
	}
}

func TestCreateDebt_ZeroAmount(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348080000004")
	customerID := createTestCustomer(t, env, userID, "Chinedu")

	rec := env.do(t, http.MethodPost, "/api/debts", userID, map[string]any{
		"customer_id":  customerID,
		"amount_minor": 0,
	}, nil)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", rec.Code)
	}
}

func TestCreateDebt_CustomerNotOwnedByUser(t *testing.T) {
	env := newTestEnv(t)
	owner := env.newUser(t, "+2348080000005")
	attacker := env.newUser(t, "+2348080000006")
	customerID := createTestCustomer(t, env, owner, "Ngozi")

	rec := env.do(t, http.MethodPost, "/api/debts", attacker, map[string]any{
		"customer_id":  customerID,
		"amount_minor": 1000000,
	}, nil)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404 (spec §35)", rec.Code)
	}
}

func TestListDebts_ScopedToUser(t *testing.T) {
	env := newTestEnv(t)
	userA := env.newUser(t, "+2348080000007")
	userB := env.newUser(t, "+2348080000008")
	custA := createTestCustomer(t, env, userA, "Ada")
	custB := createTestCustomer(t, env, userB, "Musa")

	env.do(t, http.MethodPost, "/api/debts", userA, map[string]any{"customer_id": custA, "amount_minor": 1000000}, nil)
	env.do(t, http.MethodPost, "/api/debts", userB, map[string]any{"customer_id": custB, "amount_minor": 2000000}, nil)

	rec := env.do(t, http.MethodGet, "/api/debts", userA, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	list := decodeBody[[]map[string]any](t, rec)
	if len(list) != 1 {
		t.Fatalf("got %d debts, want 1 (scoped to user A)", len(list))
	}
}
