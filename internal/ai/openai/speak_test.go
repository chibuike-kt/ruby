package openai

import (
	"context"
	"os"
	"testing"
)

// TestSpeaker_Live_RealAPICall is docs/BRIEF-research-hardening-
// standard.md Part 5 live-testing finding #3's own ask: a live-
// equivalent test that actually hits the real OpenAI endpoint, so a
// wrong model name — the exact root cause here, "gpt-speech" was never
// a real model and returned a 404 in production while every mocked
// test in this codebase kept passing — can't silently regress again.
// Skipped, not failed, when no real API key is configured (most CI
// runs, local dev without one): this is deliberately the one test in
// this package that costs real money and needs real credentials to run
// at all, so it stays opt-in rather than blocking everyone else's runs.
func TestSpeaker_Live_RealAPICall(t *testing.T) {
	apiKey := os.Getenv("AI_PROVIDER_API_KEY")
	if apiKey == "" {
		t.Skip("AI_PROVIDER_API_KEY not set — skipping live OpenAI TTS call")
	}

	s := NewSpeaker(apiKey)
	audio, mimeType, err := s.Speak(context.Background(), "Test.")
	if err != nil {
		t.Fatalf("Speak: %v — if this is a 404, speechModel (%q) is not a real OpenAI TTS model", err, speechModel)
	}
	if len(audio) == 0 {
		t.Fatal("got 0 audio bytes back from a real TTS call")
	}
	if mimeType != speechMimeType {
		t.Fatalf("got mime type %q, want %q", mimeType, speechMimeType)
	}
	// The whole reason this codebase requests response_format=opus
	// (docs/BRIEF-research-hardening-standard.md Part 5 live-testing):
	// only a genuine Ogg container renders as a real WhatsApp voice-note
	// bubble, not a downloadable file attachment. "OggS" is Ogg's own
	// magic number — verifying it here means a future response-format
	// change that silently starts returning bare Opus frames (no
	// container) fails this test instead of shipping unnoticed.
	if len(audio) < 4 || string(audio[:4]) != "OggS" {
		got := audio
		if len(got) > 4 {
			got = got[:4]
		}
		t.Fatalf("got audio starting with %q, want the Ogg container magic bytes \"OggS\"", got)
	}
}
