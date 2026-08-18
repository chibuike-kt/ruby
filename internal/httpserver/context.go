package httpserver

import (
	"net/http"

	"github.com/chibuike-kt/ruby/internal/customer"
)

// rememberLastCustomer is a best-effort side effect after a debt or
// payment has already committed in Postgres (spec §8 signal 4). It
// never affects the response: a Redis failure here is logged and
// swallowed, since the financial mutation this follows has already
// succeeded and must not be undone or reported as failed because of it.
func (s *Server) rememberLastCustomer(r *http.Request, userID, customerID int64) {
	if s.Redis == nil {
		return
	}
	if err := customer.SetLastCustomerContext(r.Context(), s.Redis, userID, customerID, customer.DefaultLastCustomerContextTTL); err != nil && s.Logger != nil {
		s.Logger.Warn("failed to set conversational context", "user_id", userID)
	}
}
