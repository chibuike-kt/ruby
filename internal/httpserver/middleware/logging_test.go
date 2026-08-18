package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
)

// A bare status code isn't diagnosable on its own — this is the gap
// that made a real 500 (a misconfigured DATABASE_URL) silent in
// production logs. RequestLogger must surface the actual Go error
// whenever a handler reports one via SetServerError.
func TestRequestLogger_LogsErrorOn5xx(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := middleware.RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		middleware.SetServerError(r.Context(), errors.New("failed to connect to postgres: dial tcp: no such host"))
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/customers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	out := buf.String()
	if !strings.Contains(out, `"level":"ERROR"`) {
		t.Fatalf("got log %q, want ERROR level for a 5xx response", out)
	}
	if !strings.Contains(out, "failed to connect to postgres") {
		t.Fatalf("got log %q, want the underlying error message present and diagnosable", out)
	}
	if !strings.Contains(out, `"status":500`) {
		t.Fatalf("got log %q, want status 500 recorded", out)
	}
}

func TestRequestLogger_NoErrorLoggedOn2xx(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := middleware.RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	out := buf.String()
	if !strings.Contains(out, `"level":"INFO"`) {
		t.Fatalf("got log %q, want INFO level for a 2xx response", out)
	}
	if strings.Contains(out, `"error"`) {
		t.Fatalf("got log %q, want no error field for a successful request", out)
	}
}

// A handler that never calls SetServerError but still fails (e.g. a
// panic recovered elsewhere, or a 5xx written without an attached
// error) must not crash the logger — it should log without an error
// field rather than log a nil.
func TestRequestLogger_5xxWithoutServerError_LogsWithoutErrorField(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := middleware.RequestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/customers", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("got log %q, want ERROR level for a 5xx response even without an attached error", buf.String())
	}
}
