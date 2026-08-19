package whatsapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chibuike-kt/ruby/internal/dbtest"
)

func withTestGraphAPI(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	original := graphAPIBaseURL
	graphAPIBaseURL = srv.URL
	t.Cleanup(func() { graphAPIBaseURL = original })
}

func TestSendText_Success(t *testing.T) {
	var gotAuth, gotPath, gotTo, gotBody string
	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path

		var payload sendTextRequest
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		gotTo = payload.To
		gotBody = payload.Text.Body

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.OUTBOUND123"}]}`))
	})

	id, err := sendText(context.Background(), "test-token", "1234567890", "+2348012345678", "Debt recorded")
	if err != nil {
		t.Fatalf("sendText: %v", err)
	}
	if id != "wamid.OUTBOUND123" {
		t.Fatalf("got id %q, want wamid.OUTBOUND123", id)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("got Authorization %q, want Bearer test-token", gotAuth)
	}
	if gotPath != "/1234567890/messages" {
		t.Fatalf("got path %q, want /1234567890/messages", gotPath)
	}
	if gotTo != "2348012345678" {
		t.Fatalf("got to %q, want digits-only 2348012345678 (no leading +)", gotTo)
	}
	if gotBody != "Debt recorded" {
		t.Fatalf("got body %q, want %q", gotBody, "Debt recorded")
	}
}

func TestSendText_NonOKStatus(t *testing.T) {
	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	if _, err := sendText(context.Background(), "bad-token", "123", "+234", "hi"); err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
}

func TestDownloadMedia_Success(t *testing.T) {
	const audioBytes = "not-really-ogg-bytes"
	var cdnHit bool

	mux := http.NewServeMux()
	var cdnURL string

	// The download URL is only known once the test server is listening,
	// so the lookup handler builds it from cdnURL, filled in below.
	mux.HandleFunc("/media-id", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("media lookup missing bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + cdnURL + `/cdn/media-id","mime_type":"audio/ogg"}`))
	})
	mux.HandleFunc("/cdn/media-id", func(w http.ResponseWriter, r *http.Request) {
		cdnHit = true
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("media download missing bearer token")
		}
		_, _ = w.Write([]byte(audioBytes))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cdnURL = srv.URL
	original := graphAPIBaseURL
	graphAPIBaseURL = srv.URL
	t.Cleanup(func() { graphAPIBaseURL = original })

	data, mimeType, err := downloadMedia(context.Background(), "test-token", "media-id")
	if err != nil {
		t.Fatalf("downloadMedia: %v", err)
	}
	if string(data) != audioBytes {
		t.Fatalf("got data %q, want %q", data, audioBytes)
	}
	if mimeType != "audio/ogg" {
		t.Fatalf("got mime type %q, want audio/ogg", mimeType)
	}
	if !cdnHit {
		t.Fatal("expected the CDN download URL to be hit")
	}
}

func TestDownloadMedia_LookupFails(t *testing.T) {
	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	if _, _, err := downloadMedia(context.Background(), "test-token", "missing-id"); err == nil {
		t.Fatal("expected an error when the media lookup 404s, got nil")
	}
}

// TestService_SendText_PersistsOutboundMessage proves Service.SendText
// (not just the free sendText function) records the reply as an
// outbound row, symmetric with how an inbound message is stored — spec
// §39's audit trail.
func TestService_SendText_PersistsOutboundMessage(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348070000001")

	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.OUTBOUND-PERSIST"}]}`))
	})

	svc := NewService(pool, rdb, "test-secret", "test-verify-token", "test-access-token", "test-phone-number-id", slog.Default())
	if err := svc.SendText(context.Background(), "+2348070000001", "Debt recorded"); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	var gotUserID int64
	var direction, messageType, status string
	var content *string
	err := pool.QueryRow(context.Background(),
		`SELECT user_id, direction, message_type, content_reference, processing_status FROM messages WHERE provider_message_id = $1`,
		"wamid.OUTBOUND-PERSIST",
	).Scan(&gotUserID, &direction, &messageType, &content, &status)
	if err != nil {
		t.Fatalf("query stored outbound message: %v", err)
	}

	if gotUserID != userID {
		t.Fatalf("got user_id %d, want %d", gotUserID, userID)
	}
	if direction != directionOutbound {
		t.Fatalf("got direction %q, want %q", direction, directionOutbound)
	}
	if content == nil || *content != "Debt recorded" {
		t.Fatalf("got content %v, want \"Debt recorded\"", content)
	}
	if status != statusSent {
		t.Fatalf("got status %q, want %q", status, statusSent)
	}
}

func TestService_SendText_UnknownRecipient_StillPersists(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)

	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.OUTBOUND-UNKNOWN"}]}`))
	})

	svc := NewService(pool, rdb, "test-secret", "test-verify-token", "test-access-token", "test-phone-number-id", slog.Default())
	if err := svc.SendText(context.Background(), "+2348070000099", "hello"); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	var gotUserID *int64
	err := pool.QueryRow(context.Background(),
		`SELECT user_id FROM messages WHERE provider_message_id = $1`, "wamid.OUTBOUND-UNKNOWN",
	).Scan(&gotUserID)
	if err != nil {
		t.Fatalf("query stored outbound message: %v", err)
	}
	if gotUserID != nil {
		t.Fatalf("got user_id %d for an unrecognized recipient, want nil (best-effort attribution)", *gotUserID)
	}
}
