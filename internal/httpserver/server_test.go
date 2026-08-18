package httpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/httpserver"
	"github.com/chibuike-kt/ruby/internal/payment"
)

// testEnv bundles a wired router with the raw pool/redis handles tests
// need for fixtures (creating users, customers) that the HTTP API
// itself doesn't expose a way to seed directly (e.g. a specific phone
// number for two same-named customers).
type testEnv struct {
	router http.Handler
	pool   *pgxpool.Pool
	redis  *redis.Client
}

func newTestEnv(t *testing.T) testEnv {
	t.Helper()
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)

	srv := &httpserver.Server{
		Pool:               pool,
		Redis:              rdb,
		Customers:          customer.NewService(pool),
		Debts:              debt.NewService(pool),
		Payments:           payment.NewService(pool),
		RateLimitPerMinute: 1000, // high enough that handler tests don't trip it; rate limiting has its own tests
		Logger:             slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	return testEnv{router: httpserver.NewRouter(srv), pool: pool, redis: rdb}
}

func (e testEnv) newUser(t *testing.T, phone string) int64 {
	t.Helper()
	return dbtest.CreateUser(t, e.pool, phone)
}

func (e testEnv) do(t *testing.T, method, path string, userID int64, body any, extraHeaders map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}

	req := httptest.NewRequestWithContext(context.Background(), method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if userID != 0 {
		req.Header.Set("X-User-ID", strconv.FormatInt(userID, 10))
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
	}
	return v
}

func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
