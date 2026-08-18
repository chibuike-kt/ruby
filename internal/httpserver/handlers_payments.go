package httpserver

import (
	"net/http"

	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
	"github.com/chibuike-kt/ruby/internal/money"
)

type recordPaymentRequest struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

func (s *Server) recordPayment(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	debtID, err := parseIDParam(r, "id")
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid debt id")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		httpjson.WriteError(w, http.StatusBadRequest, "Idempotency-Key header is required")
		return
	}

	var req recordPaymentRequest
	if err := decodeJSON(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	currency := money.NGN
	if req.Currency != "" {
		currency = money.Currency(req.Currency)
	}

	result, err := s.Payments.Record(r.Context(), userID, debtID, money.New(req.AmountMinor, currency), idempotencyKey)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	s.rememberLastCustomer(r, userID, result.Debt.CustomerID)

	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	httpjson.Write(w, status, toPaymentResponse(result.Payment, result.Debt))
}
