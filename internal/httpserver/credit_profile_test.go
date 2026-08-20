package httpserver_test

import (
	"net/http"
	"testing"
	"time"
)

func TestSetCreditProfileSharing_TogglesOwnAccount(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000001")

	rec := env.do(t, http.MethodPatch, "/api/credit-profile-sharing", userID, map[string]any{"enabled": true}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody[map[string]any](t, rec)
	if body["enabled"] != true {
		t.Fatalf("got enabled=%v, want true", body["enabled"])
	}
}

func TestSetCreditProfileSharing_MissingAuth(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(t, http.MethodPatch, "/api/credit-profile-sharing", 0, map[string]any{"enabled": true}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestGetCreditProfile_MissingBearerToken(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000002")

	rec := env.do(t, http.MethodGet, "/api/credit-profile/"+itoa(userID), 0, nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestGetCreditProfile_WrongBearerToken(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000003")

	rec := env.do(t, http.MethodGet, "/api/credit-profile/"+itoa(userID), 0, nil, map[string]string{
		"Authorization": "Bearer not-the-real-token",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestGetCreditProfile_NotOptedIn_Forbidden(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000004")

	rec := env.do(t, http.MethodGet, "/api/credit-profile/"+itoa(userID), 0, nil, map[string]string{
		"Authorization": "Bearer " + testWemaPartnerToken,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403 — trader never opted in, body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetCreditProfile_UnknownUser_NotFound(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(t, http.MethodGet, "/api/credit-profile/999999", 0, nil, map[string]string{
		"Authorization": "Bearer " + testWemaPartnerToken,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404", rec.Code)
	}
}

// TestGetCreditProfile_OptedIn_ReturnsAggregateOnly is the required
// end-to-end test: after opting in, a valid partner request gets back
// exactly the aggregate fields docs/wema-integration.md specifies —
// never a customer name, phone number, or individual transaction.
func TestGetCreditProfile_OptedIn_ReturnsAggregateOnly(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000005")

	// Opt in.
	if rec := env.do(t, http.MethodPatch, "/api/credit-profile-sharing", userID, map[string]any{"enabled": true}, nil); rec.Code != http.StatusOK {
		t.Fatalf("opt-in failed: status %d body=%s", rec.Code, rec.Body.String())
	}

	// One customer, one debt due yesterday, paid in full today (late).
	custRec := env.do(t, http.MethodPost, "/api/customers", userID, map[string]any{"name": "Chinedu", "phone_number": "+2348030000099"}, nil)
	if custRec.Code != http.StatusCreated {
		t.Fatalf("create customer failed: status %d body=%s", custRec.Code, custRec.Body.String())
	}
	custBody := decodeBody[map[string]any](t, custRec)
	customerID := int64(custBody["id"].(float64))

	dueDate := time.Now().Add(-24 * time.Hour).Format(time.DateOnly)
	debtRec := env.do(t, http.MethodPost, "/api/debts", userID, map[string]any{
		"customer_id":  customerID,
		"amount_minor": 7500000,
		"description":  "rice",
		"due_date":     dueDate,
	}, nil)
	if debtRec.Code != http.StatusCreated {
		t.Fatalf("create debt failed: status %d body=%s", debtRec.Code, debtRec.Body.String())
	}
	debtBody := decodeBody[map[string]any](t, debtRec)
	debtID := int64(debtBody["id"].(float64))

	payRec := env.do(t, http.MethodPost, "/api/debts/"+itoa(debtID)+"/payments", userID, map[string]any{
		"amount_minor": 7500000,
	}, map[string]string{"Idempotency-Key": "test-key-credit-profile-1"})
	if payRec.Code != http.StatusCreated {
		t.Fatalf("record payment failed: status %d body=%s", payRec.Code, payRec.Body.String())
	}

	rec := env.do(t, http.MethodGet, "/api/credit-profile/"+itoa(userID), 0, nil, map[string]string{
		"Authorization": "Bearer " + testWemaPartnerToken,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	body := decodeBody[map[string]any](t, rec)

	wantFields := map[string]bool{
		"trader_name": true, "account_age_days": true, "total_credit_issued_minor": true,
		"total_collected_minor": true, "on_time_payment_rate": true, "active_customer_count": true,
		"outstanding_minor": true,
	}
	for field := range body {
		if !wantFields[field] {
			t.Fatalf("got unexpected field %q in credit profile response — must be aggregate-only", field)
		}
	}
	forbidden := []string{"customer", "customers", "phone", "phone_number", "name", "debts", "payments", "transactions"}
	for _, f := range forbidden {
		if _, ok := body[f]; ok {
			t.Fatalf("got forbidden field %q in credit profile response — must never expose customer identity or transaction detail", f)
		}
	}

	if body["total_credit_issued_minor"] != float64(7500000) {
		t.Fatalf("got total_credit_issued_minor %v, want 7500000", body["total_credit_issued_minor"])
	}
	if body["total_collected_minor"] != float64(7500000) {
		t.Fatalf("got total_collected_minor %v, want 7500000", body["total_collected_minor"])
	}
	if body["outstanding_minor"] != float64(0) {
		t.Fatalf("got outstanding_minor %v, want 0", body["outstanding_minor"])
	}
	if body["active_customer_count"] != float64(0) {
		t.Fatalf("got active_customer_count %v, want 0 — the debt is fully settled", body["active_customer_count"])
	}
	if body["on_time_payment_rate"] != float64(0) {
		t.Fatalf("got on_time_payment_rate %v, want 0 — the only payment was made after its due date", body["on_time_payment_rate"])
	}
}

// TestGetCreditProfile_RevokedAccess_ForbiddenAgain confirms consent is
// genuinely revocable, not a one-way switch.
func TestGetCreditProfile_RevokedAccess_ForbiddenAgain(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348090000006")

	env.do(t, http.MethodPatch, "/api/credit-profile-sharing", userID, map[string]any{"enabled": true}, nil)
	if rec := env.do(t, http.MethodGet, "/api/credit-profile/"+itoa(userID), 0, nil, map[string]string{
		"Authorization": "Bearer " + testWemaPartnerToken,
	}); rec.Code != http.StatusOK {
		t.Fatalf("got status %d after opting in, want 200", rec.Code)
	}

	env.do(t, http.MethodPatch, "/api/credit-profile-sharing", userID, map[string]any{"enabled": false}, nil)
	rec := env.do(t, http.MethodGet, "/api/credit-profile/"+itoa(userID), 0, nil, map[string]string{
		"Authorization": "Bearer " + testWemaPartnerToken,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d after revoking, want 403", rec.Code)
	}
}
