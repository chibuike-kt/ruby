// Package httpserver wraps Slice 1's customer/debt/payment/ledger
// services with an HTTP API (spec §43). It never duplicates their
// business logic — every handler validates just enough to build a
// request, then hands off to the existing service or free function and
// translates the result (or error) to a response.
package httpserver

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
	"github.com/chibuike-kt/ruby/internal/payment"
)

const rateLimitWindow = time.Minute

type Server struct {
	Pool               *pgxpool.Pool
	Redis              *redis.Client
	Customers          *customer.Service
	Debts              *debt.Service
	Payments           *payment.Service
	RateLimitPerMinute int
	Logger             *slog.Logger
}

func NewRouter(s *Server) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.RequestID, chimw.Recoverer, middleware.RequestLogger(s.Logger))

	// /healthz must be reachable with no headers at all — that's the
	// whole point of a health check for orchestration tooling — so it's
	// registered before TempAuth is added to the chain, not inside the
	// /api group below.
	r.Get("/healthz", healthz)

	rateLimited := middleware.RateLimit(s.Redis, s.RateLimitPerMinute, rateLimitWindow)

	r.Route("/api", func(r chi.Router) {
		r.Use(middleware.TempAuth(s.Pool))

		r.Post("/customers", s.createCustomer)
		r.Get("/customers", s.listCustomers)
		r.Get("/customers/{id}", s.getCustomer)

		r.With(rateLimited).Post("/debts", s.createDebt)
		r.Get("/debts", s.listDebts)

		r.With(rateLimited).Post("/debts/{id}/payments", s.recordPayment)

		r.Get("/ledger", s.listLedger)
		r.Get("/summary", s.summary)
	})

	return r
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	httpjson.Write(w, http.StatusOK, map[string]string{"status": "ok"})
}
