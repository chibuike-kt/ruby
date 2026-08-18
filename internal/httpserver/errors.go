package httpserver

import (
	"errors"
	"net/http"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
	"github.com/chibuike-kt/ruby/internal/money"
	"github.com/chibuike-kt/ruby/internal/payment"
)

// writeServiceError maps a Slice 1 service error to an HTTP response.
// It never invents new business rules — it only reads the errors those
// services already define and picks the matching status code (spec
// §37: no unexplained technical errors).
func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var ambiguous *customer.AmbiguousError
	var overpay *payment.OverpaymentError
	var idemConflict *payment.IdempotencyConflictError

	switch {
	case errors.Is(err, customer.ErrNotFound), errors.Is(err, debt.ErrNotFound), errors.Is(err, payment.ErrNotFound):
		httpjson.WriteError(w, http.StatusNotFound, "not found")

	case errors.As(err, &ambiguous):
		httpjson.Write(w, http.StatusConflict, map[string]any{
			"error":      "ambiguous customer, disambiguation required",
			"candidates": toCustomerResponses(ambiguous.Candidates),
		})

	case errors.As(err, &overpay):
		httpjson.Write(w, http.StatusConflict, map[string]any{
			"error":             "payment exceeds outstanding balance",
			"outstanding_minor": overpay.Outstanding.MinorUnits(),
			"attempted_minor":   overpay.Attempted.MinorUnits(),
			"currency":          string(overpay.Outstanding.Currency()),
		})

	case errors.As(err, &idemConflict):
		httpjson.WriteError(w, http.StatusConflict, "idempotency key already used for a different payment")

	case errors.Is(err, payment.ErrDebtSettled):
		httpjson.WriteError(w, http.StatusConflict, "debt is already settled")

	case errors.Is(err, payment.ErrInvalidAmount),
		errors.Is(err, debt.ErrInvalidAmount),
		errors.Is(err, payment.ErrIdempotencyKeyRequired),
		errors.Is(err, customer.ErrInvalidName),
		errors.Is(err, customer.ErrNoIdentitySignal),
		errors.Is(err, customer.ErrDuplicateNameRequiresSignal),
		errors.Is(err, money.ErrCurrencyMismatch):
		httpjson.WriteError(w, http.StatusBadRequest, err.Error())

	default:
		middleware.SetServerError(r.Context(), err)
		httpjson.WriteError(w, http.StatusInternalServerError, "something went wrong while recording that, your existing records are safe")
	}
}
