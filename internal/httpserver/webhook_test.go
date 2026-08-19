package httpserver_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func webhookTextPayload(from, id string) []byte {
	return fmt.Appendf(nil, `{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "entry-1",
			"changes": [{
				"field": "messages",
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "1", "phone_number_id": "1"},
					"contacts": [{"profile": {"name": "Trader"}, "wa_id": %q}],
					"messages": [{"from": %q, "id": %q, "timestamp": "1749416383", "type": "text", "text": {"body": "hello"}}]
				}
			}]
		}]
	}`, from, from, id)
}

// doWebhookPost fires a raw POST with a hand-set signature header, bypassing
// env.do (which is built for the trader-facing JSON API and always sets
// X-User-ID — Meta never sends that header at all).
func doWebhookPost(t *testing.T, env testEnv, body []byte, signature string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/webhooks/whatsapp", bytes.NewReader(body))
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)
	return rec
}

func TestWhatsAppWebhook_GetHandshake_CorrectToken(t *testing.T) {
	env := newTestEnv(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token="+testWhatsAppVerifyToken+"&hub.challenge=12345", nil)
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "12345" {
		t.Fatalf("got body %q, want the echoed challenge %q", rec.Body.String(), "12345")
	}
}

func TestWhatsAppWebhook_GetHandshake_WrongToken(t *testing.T) {
	env := newTestEnv(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=12345", nil)
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403", rec.Code)
	}
}

// The webhook must never require the trader-facing auth header — Meta
// doesn't send one.
func TestWhatsAppWebhook_GetHandshake_NoXUserIDNeeded(t *testing.T) {
	env := newTestEnv(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/webhooks/whatsapp?hub.mode=subscribe&hub.verify_token="+testWhatsAppVerifyToken+"&hub.challenge=1", nil)
	// deliberately no X-User-ID header
	rec := httptest.NewRecorder()
	env.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 with no X-User-ID header at all", rec.Code)
	}
}

func TestWhatsAppWebhook_Post_ValidSignature_Stores(t *testing.T) {
	env := newTestEnv(t)
	userID := env.newUser(t, "+16505551300")

	body := webhookTextPayload("16505551300", "wamid.http1")
	rec := doWebhookPost(t, env, body, signBody(testWhatsAppSecret, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	var storedUserID int64
	err := env.pool.QueryRow(context.Background(),
		`SELECT user_id FROM messages WHERE provider_message_id = $1`, "wamid.http1").Scan(&storedUserID)
	if err != nil {
		t.Fatalf("query stored message: %v", err)
	}
	if storedUserID != userID {
		t.Fatalf("got user_id %d, want %d", storedUserID, userID)
	}
}

func TestWhatsAppWebhook_Post_InvalidSignature_Rejected(t *testing.T) {
	env := newTestEnv(t)

	body := webhookTextPayload("16505551301", "wamid.http2")
	rec := doWebhookPost(t, env, body, signBody("wrong-secret", body))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403", rec.Code)
	}

	var count int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM messages WHERE provider_message_id = $1`, "wamid.http2").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Fatal("a request with an invalid signature must never be stored")
	}
}

func TestWhatsAppWebhook_Post_MissingSignature_Rejected(t *testing.T) {
	env := newTestEnv(t)

	body := webhookTextPayload("16505551302", "wamid.http3")
	rec := doWebhookPost(t, env, body, "")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("got status %d, want 403", rec.Code)
	}
}

func TestWhatsAppWebhook_Post_MalformedPayload_ButValidSignature(t *testing.T) {
	env := newTestEnv(t)

	body := []byte(`{not valid json`)
	rec := doWebhookPost(t, env, body, signBody(testWhatsAppSecret, body))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got status %d, want 400 (signature was valid, payload wasn't)", rec.Code)
	}
}

// TestWhatsAppWebhook_Post_NewSender_Returns200AndAutoCreatesUser covers
// docs/BRIEF-response-quality.md #1 through the HTTP layer: a message
// from a phone number with no matching users row auto-creates one
// (empty name — internal/ai's name-capture flow fills it in) rather
// than being stuck at an UNKNOWN_USER dead end.
func TestWhatsAppWebhook_Post_NewSender_Returns200AndAutoCreatesUser(t *testing.T) {
	env := newTestEnv(t)

	body := webhookTextPayload("16505559999", "wamid.http4")
	rec := doWebhookPost(t, env, body, signBody(testWhatsAppSecret, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200 for a new sender, body=%s", rec.Code, rec.Body.String())
	}

	var status string
	var userID *int64
	err := env.pool.QueryRow(context.Background(),
		`SELECT processing_status, user_id FROM messages WHERE provider_message_id = $1`, "wamid.http4").
		Scan(&status, &userID)
	if err != nil {
		t.Fatalf("query stored message: %v", err)
	}
	if status != "RECEIVED" {
		t.Fatalf("got processing_status %q, want RECEIVED", status)
	}
	if userID == nil {
		t.Fatal("got a nil user_id, want the auto-created user's id")
	}

	var name string
	if err := env.pool.QueryRow(context.Background(), `SELECT name FROM users WHERE id = $1`, *userID).Scan(&name); err != nil {
		t.Fatalf("query auto-created user: %v", err)
	}
	if name != "" {
		t.Fatalf("got name %q for a brand-new user, want empty (asked for, never guessed)", name)
	}
}

func TestWhatsAppWebhook_Post_DuplicateDelivery_SecondCallStillReturns200NoSecondRow(t *testing.T) {
	env := newTestEnv(t)
	env.newUser(t, "+16505551303")

	body := webhookTextPayload("16505551303", "wamid.http5")
	sig := signBody(testWhatsAppSecret, body)

	first := doWebhookPost(t, env, body, sig)
	if first.Code != http.StatusOK {
		t.Fatalf("first delivery: got status %d, want 200", first.Code)
	}

	second := doWebhookPost(t, env, body, sig)
	if second.Code != http.StatusOK {
		t.Fatalf("second (duplicate) delivery: got status %d, want 200 so Meta stops retrying", second.Code)
	}

	var count int
	if err := env.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM messages WHERE provider_message_id = $1`, "wamid.http5").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d rows for a duplicate delivery, want exactly 1", count)
	}
}
