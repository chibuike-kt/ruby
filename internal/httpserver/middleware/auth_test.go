package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
)

func TestTempAuth_MissingHeader(t *testing.T) {
	pool := dbtest.Open(t)
	chain := middleware.TempAuth(pool)(passThrough())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/customers", nil)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestTempAuth_UnknownUser(t *testing.T) {
	pool := dbtest.Open(t)
	chain := middleware.TempAuth(pool)(passThrough())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/customers", nil)
	req.Header.Set("X-User-ID", "999999")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestTempAuth_MalformedHeader(t *testing.T) {
	pool := dbtest.Open(t)
	chain := middleware.TempAuth(pool)(passThrough())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/customers", nil)
	req.Header.Set("X-User-ID", "not-a-number")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestTempAuth_ValidUser(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000004")
	chain := middleware.TempAuth(pool)(passThrough())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/customers", nil)
	req.Header.Set("X-User-ID", strconv.FormatInt(userID, 10))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
}

func passThrough() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := middleware.UserIDFromContext(r.Context())
		if !ok || userID == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}
