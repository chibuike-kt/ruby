package httpserver

import (
	"net/http"
	"sort"

	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
	"github.com/chibuike-kt/ruby/internal/ledger"
)

// summary derives its totals from ledger.ListByUser rather than adding
// a new service method: every debt creation and payment is already a
// signed ledger entry (spec §16), so
//
//	outstanding = issued - collected
//
// holds across the whole user without a per-debt loop.
func (s *Server) summary(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	entries, err := ledger.ListByUser(r.Context(), s.Pool, userID)
	if err != nil {
		writeServiceError(w, err)
		return
	}

	byCurrency := map[string]*summaryTotals{}
	for _, e := range entries {
		t := byCurrency[e.Currency]
		if t == nil {
			t = &summaryTotals{Currency: e.Currency}
			byCurrency[e.Currency] = t
		}
		switch e.Type {
		case ledger.EntryDebtCreated:
			t.TotalCreditIssuedMinor += e.AmountMinor
		case ledger.EntryPaymentRecorded:
			t.TotalCollectedMinor += -e.AmountMinor
		}
	}

	resp := make([]summaryTotals, 0, len(byCurrency))
	for _, t := range byCurrency {
		t.TotalOutstandingMinor = t.TotalCreditIssuedMinor - t.TotalCollectedMinor
		resp = append(resp, *t)
	}
	sort.Slice(resp, func(i, j int) bool { return resp[i].Currency < resp[j].Currency })

	httpjson.Write(w, http.StatusOK, resp)
}
