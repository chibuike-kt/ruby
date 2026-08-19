package whatsapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
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

func TestSendInteractiveButtons_RequestShape(t *testing.T) {
	var gotReq interactiveButtonRequest
	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.BTN123"}]}`))
	})

	buttons := []ai.Button{{ID: "42", Title: "1. Chinedu"}, {ID: "confirm", Title: "Confirm"}}
	id, err := sendInteractiveButtons(context.Background(), "test-token", "1234567890", "+2348012345678", "Which one?", buttons)
	if err != nil {
		t.Fatalf("sendInteractiveButtons: %v", err)
	}
	if id != "wamid.BTN123" {
		t.Fatalf("got id %q, want wamid.BTN123", id)
	}
	if gotReq.Type != "interactive" || gotReq.Interactive.Type != "button" {
		t.Fatalf("got type=%q interactive.type=%q, want interactive/button", gotReq.Type, gotReq.Interactive.Type)
	}
	if gotReq.Interactive.Body.Text != "Which one?" {
		t.Fatalf("got body text %q, want %q", gotReq.Interactive.Body.Text, "Which one?")
	}
	if len(gotReq.Interactive.Action.Buttons) != 2 {
		t.Fatalf("got %d buttons, want 2", len(gotReq.Interactive.Action.Buttons))
	}
	first := gotReq.Interactive.Action.Buttons[0]
	if first.Type != "reply" || first.Reply.ID != "42" || first.Reply.Title != "1. Chinedu" {
		t.Fatalf("got button %+v, want {reply {42 1. Chinedu}}", first)
	}
}

// TestSendInteractiveButtons_TruncatesLongTitles is the required test:
// a button title built from unbounded data (e.g. a long customer name)
// must never reach the Cloud API over the 20-character safe limit
// (docs/BRIEF-interactive-messages.md), regardless of what the caller
// passed in.
func TestSendInteractiveButtons_TruncatesLongTitles(t *testing.T) {
	var gotReq interactiveButtonRequest
	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.BTN124"}]}`))
	})

	longTitle := "1. Chukwuemeka Okonkwo-Adeyemi" // 30 chars, well past the 20-char safe limit
	buttons := []ai.Button{{ID: "99", Title: longTitle}}
	if _, err := sendInteractiveButtons(context.Background(), "test-token", "123", "+234", "Which one?", buttons); err != nil {
		t.Fatalf("sendInteractiveButtons: %v", err)
	}

	gotTitle := gotReq.Interactive.Action.Buttons[0].Reply.Title
	if len([]rune(gotTitle)) > 20 {
		t.Fatalf("got title %q (%d runes), want at most 20", gotTitle, len([]rune(gotTitle)))
	}
	if gotTitle == longTitle {
		t.Fatalf("got the untruncated title %q sent to the Cloud API, want it truncated", gotTitle)
	}
	if !strings.HasPrefix(longTitle, gotTitle[:len(gotTitle)-len("…")]) {
		t.Fatalf("got truncated title %q, want it to be a prefix of the original %q plus an ellipsis", gotTitle, longTitle)
	}
}

func TestSendInteractiveButtons_NonOKStatus(t *testing.T) {
	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	if _, err := sendInteractiveButtons(context.Background(), "test-token", "123", "+234", "body", []ai.Button{{ID: "1", Title: "A"}}); err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
}

func TestSendInteractiveList_RequestShape(t *testing.T) {
	var gotReq interactiveListRequest
	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.LIST123"}]}`))
	})

	sections := []ai.ListSection{{
		Title: "Candidates",
		Options: []ai.ListOption{
			{ID: "1", Title: "1. Chinedu", Description: "2 cartons of noodles"},
			{ID: "2", Title: "2. Chinedu", Description: "1 bag of rice"},
		},
	}}
	id, err := sendInteractiveList(context.Background(), "test-token", "123", "+234", "Which one?", "Select", sections)
	if err != nil {
		t.Fatalf("sendInteractiveList: %v", err)
	}
	if id != "wamid.LIST123" {
		t.Fatalf("got id %q, want wamid.LIST123", id)
	}
	if gotReq.Interactive.Type != "list" || gotReq.Interactive.Action.Button != "Select" {
		t.Fatalf("got interactive.type=%q action.button=%q, want list/Select", gotReq.Interactive.Type, gotReq.Interactive.Action.Button)
	}
	if len(gotReq.Interactive.Action.Sections) != 1 || len(gotReq.Interactive.Action.Sections[0].Rows) != 2 {
		t.Fatalf("got sections %+v, want 1 section with 2 rows", gotReq.Interactive.Action.Sections)
	}
	row := gotReq.Interactive.Action.Sections[0].Rows[0]
	if row.ID != "1" || row.Title != "1. Chinedu" || row.Description != "2 cartons of noodles" {
		t.Fatalf("got row %+v, want {1 1. Chinedu 2 cartons of noodles}", row)
	}
}

func TestSendInteractiveList_TruncatesLongTitles(t *testing.T) {
	var gotReq interactiveListRequest
	withTestGraphAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.LIST124"}]}`))
	})

	longTitle := "5. Chukwuemeka Okonkwo-Adeyemi"
	sections := []ai.ListSection{{Title: "Candidates", Options: []ai.ListOption{{ID: "5", Title: longTitle}}}}
	if _, err := sendInteractiveList(context.Background(), "test-token", "123", "+234", "body", "Select", sections); err != nil {
		t.Fatalf("sendInteractiveList: %v", err)
	}

	gotTitle := gotReq.Interactive.Action.Sections[0].Rows[0].Title
	if len([]rune(gotTitle)) > 20 {
		t.Fatalf("got row title %q (%d runes), want at most 20", gotTitle, len([]rune(gotTitle)))
	}
}

func TestTruncateTitle_ShortTitleUnchanged(t *testing.T) {
	if got := truncateTitle("Confirm"); got != "Confirm" {
		t.Fatalf("got %q, want %q unchanged", got, "Confirm")
	}
}

func TestTruncateTitle_ExactlyAtLimitUnchanged(t *testing.T) {
	title := strings.Repeat("a", 20)
	if got := truncateTitle(title); got != title {
		t.Fatalf("got %q, want the 20-char title unchanged", got)
	}
}
