package httpserver

import (
	"net/http"
	"strconv"

	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
	"github.com/chibuike-kt/ruby/internal/ledger"
)

func (s *Server) listLedger(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	debtIDParam := r.URL.Query().Get("debt_id")
	if debtIDParam == "" {
		entries, err := ledger.ListByUser(r.Context(), s.Pool, userID)
		if err != nil {
			writeServiceError(w, r, err)
			return
		}
		httpjson.Write(w, http.StatusOK, toLedgerEntryResponses(entries))
		return
	}

	debtID, err := strconv.ParseInt(debtIDParam, 10, 64)
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid debt_id")
		return
	}

	// ledger.ListByDebt isn't itself scoped by user_id — ownership must
	// be checked here first (spec §35), via a lookup that is.
	if _, err := s.Debts.Get(r.Context(), userID, debtID); err != nil {
		writeServiceError(w, r, err)
		return
	}

	entries, err := ledger.ListByDebt(r.Context(), s.Pool, debtID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	httpjson.Write(w, http.StatusOK, toLedgerEntryResponses(entries))
}
