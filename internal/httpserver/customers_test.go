package httpserver_test

import (
	"net/http"
	"testing"
)

func TestCreateCustomer(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348070000001")

	rec := env.do(t, http.MethodPost, "/api/customers", userID, map[string]any{
		"name": "Chinedu",
	}, nil)

	if rec.Code != http.StatusCreated {
		t.Fatalf("got status %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	body := decodeBody[map[string]any](t, rec)
	if body["name"] != "Chinedu" {
		t.Fatalf("got name %v, want Chinedu", body["name"])
	}
}

func TestCreateCustomer_MissingAuth(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(t, http.MethodPost, "/api/customers", 0, map[string]any{"name": "Chinedu"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestCreateCustomer_EmptyName(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+2348070000002")

	rec := env.do(t, http.MethodPost, "/api/customers", userID, map[string]any{"name": "   "}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400", rec.Code)
	}
}

func TestListCustomers_ScopedToUser(t *testing.T) {
	env := newTestEnv(t)
	userA := env.newUser(t, "+2348070000003")
	userB := env.newUser(t, "+2348070000004")

	env.do(t, http.MethodPost, "/api/customers", userA, map[string]any{"name": "Ada"}, nil)
	env.do(t, http.MethodPost, "/api/customers", userB, map[string]any{"name": "Musa"}, nil)

	rec := env.do(t, http.MethodGet, "/api/customers", userA, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
	list := decodeBody[[]map[string]any](t, rec)
	if len(list) != 1 {
		t.Fatalf("got %d customers, want 1 (scoped to user A)", len(list))
	}
	if list[0]["name"] != "Ada" {
		t.Fatalf("got %v, want Ada", list[0]["name"])
	}
}

func TestGetCustomer_CrossUserAccessDenied(t *testing.T) {
	env := newTestEnv(t)
	owner := env.newUser(t, "+2348070000005")
	other := env.newUser(t, "+2348070000006")

	created := decodeBody[map[string]any](t, env.do(t, http.MethodPost, "/api/customers", owner, map[string]any{"name": "Ngozi"}, nil))
	id := int64(created["id"].(float64))

	rec := env.do(t, http.MethodGet, "/api/customers/"+itoa(id), other, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want 404 (spec §35 cross-user protection)", rec.Code)
	}
}
