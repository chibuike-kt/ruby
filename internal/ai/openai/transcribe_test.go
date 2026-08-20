package openai

import (
	"context"
	"mime"
	"net/http"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
)

// TestTranscribe_RequestShape is also the regression test for
// docs/BRIEF-fixes-and-reminders.md #2's root cause: an earlier version
// sent a repeated "languages" field, which the live gpt-transcribe
// endpoint rejects outright (400 invalid_value) regardless of audio
// content — every voice note failed at exactly this step. Verified live
// against the real endpoint while debugging; asserting its absence here
// keeps it from regressing silently.
func TestTranscribe_RequestShape(t *testing.T) {
	var gotAuth, gotModel string
	var gotLanguagesField bool
	var gotFileBytes []byte

	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")

		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Fatalf("got content-type %q, want multipart/form-data", r.Header.Get("Content-Type"))
		}
		const maxTestUploadBytes = 1 << 20
		if err := r.ParseMultipartForm(maxTestUploadBytes); err != nil { //nolint:gosec // test-only httptest handler receiving a small synthetic fixture, not a production endpoint
			t.Fatalf("ParseMultipartForm: %v", err)
		}
		_ = params

		gotModel = r.FormValue("model")
		_, gotLanguagesField = r.MultipartForm.Value["languages"]

		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer func() { _ = file.Close() }()
		buf := make([]byte, 512)
		n, _ := file.Read(buf)
		gotFileBytes = buf[:n]

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"Chinedu took 75k","languages":[{"code":"en"}]}`))
	})

	tr := NewTranscriber("test-key")
	text, lang, err := tr.Transcribe(context.Background(), []byte("fake-mp3-bytes"))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Fatalf("got Authorization %q, want Bearer test-key", gotAuth)
	}
	if gotModel != "gpt-transcribe" {
		t.Fatalf("got model %q, want gpt-transcribe", gotModel)
	}
	if gotLanguagesField {
		t.Fatal("got a \"languages\" field on the request — the live API rejects this with a 400 on every call, see docs/BRIEF-fixes-and-reminders.md #2")
	}
	if string(gotFileBytes) != "fake-mp3-bytes" {
		t.Fatalf("got file bytes %q, want fake-mp3-bytes", gotFileBytes)
	}
	if text != "Chinedu took 75k" || lang != ai.LangEnglish {
		t.Fatalf("got (%q, %q), want (\"Chinedu took 75k\", \"en\")", text, lang)
	}
}

// TestTranscribe_NoLanguagesInResponse covers a transcript the model
// couldn't confidently attribute to a language at all — the response's
// "languages" array can be empty, and that must not be treated as an
// error, just an empty detected language.
func TestTranscribe_NoLanguagesInResponse(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"","languages":[]}`))
	})

	tr := NewTranscriber("test-key")
	text, lang, err := tr.Transcribe(context.Background(), []byte("fake-mp3-bytes"))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if text != "" || lang != "" {
		t.Fatalf("got (%q, %q), want (\"\", \"\")", text, lang)
	}
}

func TestTranscribe_NonOKStatus(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"unsupported file format"}}`))
	})

	tr := NewTranscriber("test-key")
	if _, _, err := tr.Transcribe(context.Background(), []byte("bad-bytes")); err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
}
