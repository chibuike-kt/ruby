package httpserver

import (
	"errors"
	"net/http"

	"github.com/chibuike-kt/ruby/internal/account"
	"github.com/chibuike-kt/ruby/internal/creditprofile"
	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
)

// getCreditProfile is docs/wema-integration.md's concrete deliverable:
// GET /api/credit-profile/{user_id}, authenticated as a partner (see
// middleware.PartnerAuth), never as the trader themselves — a partner
// has no X-User-ID of their own to send. It returns aggregate data only
// (creditprofile.Profile) and is gated on the trader's own
// credit_profile_sharing_enabled flag: a partner presenting a valid
// token still gets nothing for a trader who hasn't opted in — the
// token authenticates Wema as a caller, it is never itself consent.
func (s *Server) getCreditProfile(w http.ResponseWriter, r *http.Request) {
	userID, err := parseIDParam(r, "user_id")
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	acct, err := account.GetByID(r.Context(), s.Pool, userID)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			httpjson.WriteError(w, http.StatusNotFound, "not found")
			return
		}
		middleware.SetServerError(r.Context(), err)
		httpjson.WriteError(w, http.StatusInternalServerError, "something went wrong, please try again")
		return
	}
	if !acct.CreditProfileSharingEnabled {
		httpjson.WriteError(w, http.StatusForbidden, "this trader has not opted in to sharing their credit profile")
		return
	}

	profile, err := creditprofile.Get(r.Context(), s.Pool, userID)
	if err != nil {
		middleware.SetServerError(r.Context(), err)
		httpjson.WriteError(w, http.StatusInternalServerError, "something went wrong, please try again")
		return
	}
	httpjson.Write(w, http.StatusOK, profile)
}

type setCreditProfileSharingRequest struct {
	Enabled bool `json:"enabled"`
}

// setCreditProfileSharing is the trader's own consent toggle
// (docs/wema-integration.md: "a simple opt-in flag set via API is
// enough to demonstrate consent-gating exists structurally"). It's
// under the normal TempAuth-protected /api group, so it always acts on
// the authenticated trader's own account — there's no user_id in this
// route, deliberately: a trader can only ever toggle their own sharing
// preference, never anyone else's.
func (s *Server) setCreditProfileSharing(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.UserIDFromContext(r.Context())

	var req setCreditProfileSharingRequest
	if err := decodeJSON(r, &req); err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	if err := account.SetCreditProfileSharing(r.Context(), s.Pool, userID, req.Enabled); err != nil {
		middleware.SetServerError(r.Context(), err)
		httpjson.WriteError(w, http.StatusInternalServerError, "something went wrong, please try again")
		return
	}
	httpjson.Write(w, http.StatusOK, setCreditProfileSharingRequest{Enabled: req.Enabled})
}
