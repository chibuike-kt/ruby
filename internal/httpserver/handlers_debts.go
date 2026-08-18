package httpserver

import (
	"net/http"
	"time"

	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
	"github.com/chibuike-kt/ruby/internal/money"
)

type createDebtRequest struct {
	CustomerID  int64   `json:"customer_id"`
	AmountMinor int64   `json:"amount_minor"`
	Currency    string  `json:"currency"`
	Description string  `json:"description"`
	DueDate     *string `json:"due_date"` // YYYY-MM-DD
}

func (s *Server) createDebt(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	var req createDebtRequest
	if err := decodeJSON(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	currency := money.NGN
	if req.Currency != "" {
		currency = money.Currency(req.Currency)
	}

	var dueDate *time.Time
	if req.DueDate != nil && *req.DueDate != "" {
		t, err := time.Parse(time.DateOnly, *req.DueDate)
		if err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "due_date must be YYYY-MM-DD")
			return
		}
		dueDate = &t
	}

	d, err := s.Debts.Create(r.Context(), userID, req.CustomerID, money.New(req.AmountMinor, currency), req.Description, dueDate)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	s.rememberLastCustomer(r, userID, d.CustomerID)
	httpjson.Write(w, http.StatusCreated, toDebtResponse(d))
}

func (s *Server) listDebts(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	debts, err := s.Debts.ListOutstanding(r.Context(), userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	httpjson.Write(w, http.StatusOK, toDebtResponses(debts))
}
