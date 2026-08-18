package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
)

// RateLimit enforces a fixed-window request budget per authenticated
// user (spec §35), applied only to the mutation routes that opt in.
// It must run after TempAuth so UserIDFromContext resolves.
//
// A Redis error fails OPEN: rate limiting is a protective throttle, not
// a financial correctness guarantee (that's Postgres's row locking, see
// internal/payment) — an outage here should degrade to "unlimited," not
// take the API down.
func RateLimit(rdb *redis.Client, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID, ok := UserIDFromContext(r.Context())
			if !ok {
				httpjson.WriteError(w, http.StatusUnauthorized, "missing authenticated user")
				return
			}

			allowed, err := allow(r, rdb, userID, limit, window)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
				httpjson.WriteError(w, http.StatusTooManyRequests, "rate limit exceeded, try again shortly")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func allow(r *http.Request, rdb *redis.Client, userID int64, limit int, window time.Duration) (bool, error) {
	windowIndex := time.Now().Unix() / int64(window.Seconds())
	key := fmt.Sprintf("ruby:rl:%d:%d", userID, windowIndex)

	ctx := r.Context()
	count, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		if err := rdb.Expire(ctx, key, window).Err(); err != nil {
			return false, err
		}
	}
	return count <= int64(limit), nil
}
