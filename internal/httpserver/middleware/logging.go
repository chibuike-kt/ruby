package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

type requestInfoContextKey struct{}

// requestInfo is a mutable container injected into the request context
// by RequestLogger before calling next. TempAuth, running deeper in the
// chain, writes the resolved user id through the same pointer — a
// plain context.WithValue can't do this, since a downstream middleware
// deriving a new context via r.WithContext never mutates the context
// object an upstream middleware is still holding.
type requestInfo struct {
	userID    int64
	hasUserID bool
	err       error
}

func setRequestUserID(ctx context.Context, userID int64) {
	if info, ok := ctx.Value(requestInfoContextKey{}).(*requestInfo); ok {
		info.userID = userID
		info.hasUserID = true
	}
}

// SetServerError attaches the underlying Go error behind a 5xx response
// so RequestLogger can log it. Call this immediately before writing any
// 5xx response — a bare status code isn't diagnosable on its own; that
// exact gap is what made a real 500 (a misconfigured DATABASE_URL)
// silent in the logs.
func SetServerError(ctx context.Context, err error) {
	if info, ok := ctx.Value(requestInfoContextKey{}).(*requestInfo); ok {
		info.err = err
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// RequestLogger logs method/path/status/latency/user_id for every
// request (spec §36) — never amounts, phone numbers, or customer names,
// which never pass through this middleware in the first place.
func RequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			info := &requestInfo{}
			ctx := context.WithValue(r.Context(), requestInfoContextKey{}, info)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			fields := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"latency_ms", time.Since(start).Milliseconds(),
			}
			if info.hasUserID {
				fields = append(fields, "user_id", info.userID)
			}

			if rec.status >= http.StatusInternalServerError {
				if info.err != nil {
					fields = append(fields, "error", info.err.Error())
				}
				logger.Error("request", fields...)
				return
			}
			logger.Info("request", fields...)
		})
	}
}
