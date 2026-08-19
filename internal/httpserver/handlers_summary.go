package httpserver

import (
	"net/http"

	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
	"github.com/chibuike-kt/ruby/internal/ledger"
)

func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	totals, err := ledger.SummaryByUser(r.Context(), s.Pool, userID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}

	resp := make([]summaryTotals, len(totals))
	for i, t := range totals {
		resp[i] = toSummaryTotals(t)
	}
	httpjson.Write(w, http.StatusOK, resp)
}
