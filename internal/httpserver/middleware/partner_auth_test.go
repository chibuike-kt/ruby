package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestPartnerAuth_MissingHeader(t *testing.T) {
	chain := middleware.PartnerAuth("real-token")(okHandler())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/credit-profile/1", nil)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestPartnerAuth_WrongToken(t *testing.T) {
	chain := middleware.PartnerAuth("real-token")(okHandler())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/credit-profile/1", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestPartnerAuth_NonBearerScheme(t *testing.T) {
	chain := middleware.PartnerAuth("real-token")(okHandler())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/credit-profile/1", nil)
	req.Header.Set("Authorization", "real-token") // missing "Bearer " prefix
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

// TestPartnerAuth_EmptyConfiguredToken_FailsClosed mirrors
// TempAuth/VerifyHandshake's own reasoning: an unconfigured secret must
// reject everything, never accept-by-default.
func TestPartnerAuth_EmptyConfiguredToken_FailsClosed(t *testing.T) {
	chain := middleware.PartnerAuth("")(okHandler())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/credit-profile/1", nil)
	req.Header.Set("Authorization", "Bearer anything")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", rec.Code)
	}
}

func TestPartnerAuth_ValidToken(t *testing.T) {
	chain := middleware.PartnerAuth("real-token")(okHandler())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/credit-profile/1", nil)
	req.Header.Set("Authorization", "Bearer real-token")
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}
}
