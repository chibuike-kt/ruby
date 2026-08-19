package openai

import (
	"context"
	"mime"
	"net/http"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
)

func TestTranscribe_RequestShape(t *testing.T) {
	var gotAuth, gotModel string
	var gotLanguages []string
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
		gotLanguages = r.MultipartForm.Value["languages"]

		file, _, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer func() { _ = file.Close() }()
		buf := make([]byte, 512)
		n, _ := file.Read(buf)
		gotFileBytes = buf[:n]

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"Chinedu took 75k","language":"en"}`))
	})

	tr := NewTranscriber("test-key")
	text, lang, err := tr.Transcribe(context.Background(), []byte("fake-mp3-bytes"),
		[]ai.Language{ai.LangEnglish, ai.LangPidgin, ai.LangYoruba, ai.LangIgbo, ai.LangHausa})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Fatalf("got Authorization %q, want Bearer test-key", gotAuth)
	}
	if gotModel != "gpt-transcribe" {
		t.Fatalf("got model %q, want gpt-transcribe", gotModel)
	}
	if len(gotLanguages) != 5 {
		t.Fatalf("got %d languages hints, want 5 (all supported languages, never narrowed)", len(gotLanguages))
	}
	if string(gotFileBytes) != "fake-mp3-bytes" {
		t.Fatalf("got file bytes %q, want fake-mp3-bytes", gotFileBytes)
	}
	if text != "Chinedu took 75k" || lang != ai.LangEnglish {
		t.Fatalf("got (%q, %q), want (\"Chinedu took 75k\", \"en\")", text, lang)
	}
}

func TestTranscribe_NonOKStatus(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"unsupported file format"}}`))
	})

	tr := NewTranscriber("test-key")
	if _, _, err := tr.Transcribe(context.Background(), []byte("bad-bytes"), []ai.Language{ai.LangEnglish}); err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
}
