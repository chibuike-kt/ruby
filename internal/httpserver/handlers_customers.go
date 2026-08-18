package httpserver

import (
	"net/http"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
)

type createCustomerRequest struct {
	Name        string  `json:"name"`
	PhoneNumber *string `json:"phone_number"`
	Alias       *string `json:"alias"`
}

func (s *Server) createCustomer(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	var req createCustomerRequest
	if err := decodeJSON(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	c, err := s.Customers.Create(r.Context(), userID, req.Name, req.PhoneNumber, req.Alias)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusCreated, toCustomerResponse(c))
}

func (s *Server) listCustomers(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	customers, err := customer.ListByUser(r.Context(), s.Pool, userID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, toCustomerResponses(customers))
}

func (s *Server) getCustomer(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	id, err := parseIDParam(r, "id")
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid customer id")
		return
	}

	c, err := customer.GetByID(r.Context(), s.Pool, userID, id)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, toCustomerResponse(c))
}
