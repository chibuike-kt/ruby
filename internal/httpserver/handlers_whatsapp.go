package httpserver

import (
	"errors"
	"io"
	"net/http"

	"github.com/chibuike-kt/ruby/internal/httpserver/httpjson"
	"github.com/chibuike-kt/ruby/internal/httpserver/middleware"
	"github.com/chibuike-kt/ruby/internal/whatsapp"
)

// maxWebhookBodyBytes caps how much of a webhook delivery we'll read
// into memory. WhatsApp message payloads are small JSON; even a large
// batch comes nowhere near this.
const maxWebhookBodyBytes = 1 << 20 // 1 MiB

// verifyWhatsAppWebhook handles Meta's one-time GET verification
// handshake (spec §3): echo hub.challenge back as plain text on a
// matching token, 403 otherwise.
func (s *Server) verifyWhatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mode := q.Get("hub.mode")
	token := q.Get("hub.verify_token")
	challenge := q.Get("hub.challenge")

	if !s.Webhooks.VerifyHandshake(mode, token) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(challenge)) //nolint:gosec // Meta's documented handshake requires echoing hub.challenge verbatim; text/plain means it's never HTML-rendered, so it isn't a real XSS vector
}

// receiveWhatsAppWebhook handles actual event delivery (spec §3,
// §15/§31). Signature verification happens before the body is parsed
// at all — an unsigned or mis-signed request is rejected outright,
// never touching JSON decoding or the database.
func (s *Server) receiveWhatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
	if err != nil {
		httpjson.WriteError(w, http.StatusBadRequest, "could not read request body")
		return
	}

	if !s.Webhooks.VerifySignature(body, r.Header.Get("X-Hub-Signature-256")) {
		httpjson.WriteError(w, http.StatusForbidden, "invalid signature")
		return
	}

	if _, err := s.Webhooks.ReceiveEvent(r.Context(), body); err != nil {
		if errors.Is(err, whatsapp.ErrMalformedPayload) {
			httpjson.WriteError(w, http.StatusBadRequest, "malformed webhook payload")
			return
		}
		// Not malformed input — an infrastructure failure while
		// handling a valid payload. 500 here is deliberate: Meta
		// retries on failure, and dedup makes a retry safe, so letting
		// this be retry-worthy is correct, unlike a duplicate (which
		// gets 200 below, precisely so Meta stops retrying it).
		middleware.SetServerError(r.Context(), err)
		httpjson.WriteError(w, http.StatusInternalServerError, "something went wrong while recording that message")
		return
	}

	httpjson.Write(w, http.StatusOK, map[string]string{"status": "received"})
}
