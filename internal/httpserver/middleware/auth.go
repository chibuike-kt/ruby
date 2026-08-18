package middleware

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/ruby/internal/account"
	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
)

type userIDContextKey struct{}

// TempAuth is a placeholder, not the real auth layer — see
// BRIEF-api-redis.md. It trusts a caller-supplied X-User-ID header
// once resolved against the users table; replace with JWT before any
// non-trusted deployment.
func TempAuth(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := r.Header.Get("X-User-ID")
			if raw == "" {
				httpjson.WriteError(w, http.StatusUnauthorized, "missing X-User-ID header")
				return
			}
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				httpjson.WriteError(w, http.StatusUnauthorized, "invalid X-User-ID header")
				return
			}
			if _, err := account.GetByID(r.Context(), pool, id); err != nil {
				if errors.Is(err, account.ErrNotFound) {
					httpjson.WriteError(w, http.StatusUnauthorized, "unknown user")
					return
				}
				httpjson.WriteError(w, http.StatusInternalServerError, "something went wrong, please try again")
				return
			}

			setRequestUserID(r.Context(), id)
			ctx := context.WithValue(r.Context(), userIDContextKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserIDFromContext returns the authenticated user id set by TempAuth.
func UserIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(userIDContextKey{}).(int64)
	return id, ok
}
