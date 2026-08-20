package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
)

// PartnerAuth is the Credit Profile API's auth (docs/wema-integration.md)
// — a static bearer token, not the trader-facing X-User-ID scheme
// TempAuth implements. It's a stub: "in production this would be a
// proper OAuth2 client-credentials flow issued to Wema specifically,"
// per the brief this endpoint exists to demonstrate, not a claim that
// this is production-ready partner auth. An empty configured token
// always fails closed — same reasoning as VerifyHandshake's empty
// verify-token check.
func PartnerAuth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token == "" {
				httpjson.WriteError(w, http.StatusUnauthorized, "partner API not configured")
				return
			}

			auth := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(auth, prefix) {
				httpjson.WriteError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			presented := strings.TrimPrefix(auth, prefix)
			if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
				httpjson.WriteError(w, http.StatusUnauthorized, "invalid bearer token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
