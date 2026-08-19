package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func withTestServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	original := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = original })
}

func TestExtract_RequestShape(t *testing.T) {
	var gotAuth string
	var gotReq responsesRequest

	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"output": [{"type":"message","content":[{"type":"output_text","text":"{\"intent\":\"CREATE_DEBT\",\"customer_name\":\"Chinedu\",\"amount_minor\":7500000,\"description\":null,\"due_date_iso\":null,\"confidence\":\"high\",\"language\":\"en\"}"}]}]
		}`))
	})

	e := NewExtractor("test-key", "gpt-5.6-terra", "Africa/Lagos")
	intent, err := e.Extract(context.Background(), "Chinedu took 75k")
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Fatalf("got Authorization %q, want Bearer test-key", gotAuth)
	}
	if gotReq.Model != "gpt-5.6-terra" {
		t.Fatalf("got model %q, want gpt-5.6-terra", gotReq.Model)
	}
	if gotReq.Text == nil || gotReq.Text.Format.Type != "json_schema" || !gotReq.Text.Format.Strict {
		t.Fatalf("got text.format %+v, want strict json_schema", gotReq.Text)
	}
	if gotReq.Text.Format.Schema["additionalProperties"] != false {
		t.Fatalf("got schema additionalProperties %v, want false (strict mode requirement)", gotReq.Text.Format.Schema["additionalProperties"])
	}
	if len(gotReq.Input) != 2 || gotReq.Input[1].Content != "Chinedu took 75k" {
		t.Fatalf("got input %+v, want [system, user=%q]", gotReq.Input, "Chinedu took 75k")
	}

	if intent.Intent != "CREATE_DEBT" || intent.CustomerName == nil || *intent.CustomerName != "Chinedu" {
		t.Fatalf("got intent %+v, want CREATE_DEBT for Chinedu", intent)
	}
	if intent.AmountMinor == nil || *intent.AmountMinor != 7500000 {
		t.Fatalf("got amount %v, want 7500000", intent.AmountMinor)
	}
}

func TestExtract_NonOKStatus(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	})

	e := NewExtractor("test-key", "gpt-5.6-terra", "Africa/Lagos")
	if _, err := e.Extract(context.Background(), "hello"); err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
}

func TestExtract_MalformedOutputJSON(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"output": [{"type":"message","content":[{"type":"output_text","text":"not valid json"}]}]
		}`))
	})

	e := NewExtractor("test-key", "gpt-5.6-terra", "Africa/Lagos")
	if _, err := e.Extract(context.Background(), "hello"); err == nil {
		t.Fatal("expected an error for malformed output_text JSON, got nil")
	}
}

func TestNewExtractor_UnknownTimezoneFallsBackToUTC(t *testing.T) {
	// Must not panic or error — an unrecognized IANA zone name falls
	// back to UTC rather than failing extractor construction.
	e := NewExtractor("test-key", "gpt-5.6-terra", "Not/A/Real/Zone")
	if e.location == nil {
		t.Fatal("got nil location, want a fallback (UTC)")
	}
}
