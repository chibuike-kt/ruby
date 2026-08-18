package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
)

func TestRateLimit_NPlusOneRejected(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000001")

	const limit = 5
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	chain := middleware.TempAuth(pool)(middleware.RateLimit(rdb, limit, time.Minute)(final))

	var lastStatus int
	for i := range limit + 1 {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/debts", nil)
		req.Header.Set("X-User-ID", strconv.FormatInt(userID, 10))
		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, req)
		lastStatus = rec.Code

		if i < limit && rec.Code != http.StatusOK {
			t.Fatalf("request %d: got status %d, want 200 (within limit)", i+1, rec.Code)
		}
	}

	if lastStatus != http.StatusTooManyRequests {
		t.Fatalf("request %d (limit+1): got status %d, want 429", limit+1, lastStatus)
	}
}

func TestRateLimit_ScopedPerUser(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userA := dbtest.CreateUser(t, pool, "+2348060000002")
	userB := dbtest.CreateUser(t, pool, "+2348060000003")

	const limit = 1
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	chain := middleware.TempAuth(pool)(middleware.RateLimit(rdb, limit, time.Minute)(final))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/debts", nil)
	req.Header.Set("X-User-ID", strconv.FormatInt(userA, 10))
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("user A first request: got %d, want 200", rec.Code)
	}

	req = httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/debts", nil)
	req.Header.Set("X-User-ID", strconv.FormatInt(userB, 10))
	rec = httptest.NewRecorder()
	chain.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("user B first request: got %d, want 200 — a different user's budget must be independent", rec.Code)
	}
}
