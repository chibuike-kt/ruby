package httpserver_test

import (
	"net/http"
	"testing"
)

// /healthz must be reachable with no headers at all — that's the whole
// point of a health check for Docker/orchestration tooling. It must
// never require auth.
func TestHealthz_NoAuthRequired(t *testing.T) {
	env := newTestEnv(t)

	rec := env.do(t, http.MethodGet, "/healthz", 0, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 with no X-User-ID header at all", rec.Code)
	}
}
