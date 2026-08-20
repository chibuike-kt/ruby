package openai

import (
	"context"
	"net/http"
	"testing"
)

// TestExtract_RetriesOnTransientFailure is docs/BRIEF-polish-and-
// hardening.md #4's required retry/backoff behavior: a transient
// failure (here, two consecutive 503s) is retried, not surfaced
// immediately — a flaky upstream call must not fail the whole
// extraction on one bad attempt.
func TestExtract_RetriesOnTransientFailure(t *testing.T) {
	var attempts int
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"output": [{"type":"message","content":[{"type":"output_text","text":"{\"intent\":\"HELP\",\"customer_name\":null,\"amount_minor\":null,\"description\":null,\"due_date_iso\":null,\"confidence\":\"high\",\"language\":\"en\"}"}]}]
		}`))
	})

	e := NewExtractor("test-key", "gpt-5.6-terra", "Africa/Lagos")
	if _, err := e.Extract(context.Background(), "what can you do"); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("got %d attempts, want 3 (two transient 503s, then success)", attempts)
	}
}

// TestExtract_DoesNotRetryOnClientError confirms a genuine client error
// (a 4xx other than 429, e.g. a bad request or an auth failure) is
// never retried — retrying can't fix a malformed request or a bad key.
func TestExtract_DoesNotRetryOnClientError(t *testing.T) {
	var attempts int
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusUnauthorized)
	})

	e := NewExtractor("bad-key", "gpt-5.6-terra", "Africa/Lagos")
	if _, err := e.Extract(context.Background(), "hello"); err == nil {
		t.Fatal("expected an error for a 401 response, got nil")
	}
	if attempts != 1 {
		t.Fatalf("got %d attempts, want 1 — a genuine client error must never be retried", attempts)
	}
}
