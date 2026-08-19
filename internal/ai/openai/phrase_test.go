package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
)

func TestPhrase_RequestShape(t *testing.T) {
	var gotReq responsesRequest

	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"output": [{"type":"message","content":[{"type":"output_text","text":"  Debt recorded! Chinedu owes 75,000.  "}]}]
		}`))
	})

	p := NewPhraser("test-key")
	text, err := p.Phrase(context.Background(), ai.PhraseInput{
		Event:        ai.EventDebtCreated,
		Language:     ai.LangEnglish,
		CustomerName: "Chinedu",
		AmountMinor:  7500000,
	})
	if err != nil {
		t.Fatalf("Phrase: %v", err)
	}

	if gotReq.Model != "gpt-5.6-luna" {
		t.Fatalf("got model %q, want gpt-5.6-luna (the deliberately cheap tier)", gotReq.Model)
	}
	if gotReq.Text != nil {
		t.Fatalf("got a schema on the phrasing call, want none — it returns plain phrasing text")
	}
	if len(gotReq.Input) != 2 {
		t.Fatalf("got %d input messages, want 2 (system, user)", len(gotReq.Input))
	}

	var sentInput ai.PhraseInput
	if err := json.Unmarshal([]byte(gotReq.Input[1].Content), &sentInput); err != nil {
		t.Fatalf("the user message wasn't the marshaled PhraseInput: %v", err)
	}
	if sentInput.CustomerName != "Chinedu" || sentInput.AmountMinor != 7500000 {
		t.Fatalf("got %+v, want the PhraseInput round-tripped as the user message", sentInput)
	}

	if text != "Debt recorded! Chinedu owes 75,000." {
		t.Fatalf("got %q, want the response trimmed of surrounding whitespace", text)
	}
}

func TestPhrase_NonOKStatus(t *testing.T) {
	withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	p := NewPhraser("test-key")
	if _, err := p.Phrase(context.Background(), ai.PhraseInput{Event: ai.EventCustomerBalance, Language: ai.LangEnglish}); err == nil {
		t.Fatal("expected an error for a non-200 response, got nil")
	}
}
