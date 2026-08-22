package whatsapp

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai/openai"
)

// TestUploadMedia_Live_RoundTripIsGenuineOggOpus is docs/BRIEF-research-
// hardening-standard.md Part 5 live-testing's own ask: proving the
// actual uploaded media, once fetched back from Meta, is genuinely
// Ogg/Opus — not just that upload returned 200. A stub-server test can
// confirm the request shape this codebase sends; it can't confirm Meta
// actually stored and still serves back a real voice-note-renderable
// file, which is the whole point of switching off AAC in the first
// place. This chains two real calls end to end (OpenAI TTS, then
// Meta's own media upload+download) and never calls sendAudio/SendAudio
// — uploading and immediately fetching back media never delivers
// anything to a real WhatsApp recipient, so nothing here is customer-
// facing. Skipped, not failed, without real credentials for both.
func TestUploadMedia_Live_RoundTripIsGenuineOggOpus(t *testing.T) {
	openaiKey := os.Getenv("AI_PROVIDER_API_KEY")
	accessToken := os.Getenv("WHATSAPP_ACCESS_TOKEN")
	phoneNumberID := os.Getenv("WHATSAPP_PHONE_NUMBER_ID")
	if openaiKey == "" || accessToken == "" || phoneNumberID == "" {
		t.Skip("AI_PROVIDER_API_KEY/WHATSAPP_ACCESS_TOKEN/WHATSAPP_PHONE_NUMBER_ID not set — skipping live TTS+Meta media round-trip")
	}

	speaker := openai.NewSpeaker(openaiKey)
	audio, mimeType, err := speaker.Speak(context.Background(), "Test.")
	if err != nil {
		t.Fatalf("Speak: %v", err)
	}
	if len(audio) < 4 || string(audio[:4]) != "OggS" {
		t.Fatalf("got synthesized audio that isn't a genuine Ogg container — this test needs a real Ogg/Opus payload to prove anything about the upload round-trip")
	}

	mediaID, err := uploadMedia(context.Background(), accessToken, phoneNumberID, audio, mimeType)
	if err != nil {
		t.Fatalf("uploadMedia: %v — a 400 here is likely error 131053, a declared-type/actual-file-type mismatch", err)
	}
	if mediaID == "" {
		t.Fatal("got an empty media id from a successful-looking upload")
	}

	downloaded, downloadedMimeType, err := downloadMedia(context.Background(), accessToken, mediaID)
	if err != nil {
		t.Fatalf("downloadMedia: %v", err)
	}
	// The actual proof this test exists for: not "upload returned 200,"
	// but "what Meta now serves back for this media id starts with
	// Ogg's own magic bytes" — the container WhatsApp's client actually
	// needs to render a real voice-note bubble instead of a generic file
	// attachment.
	if len(downloaded) < 4 || string(downloaded[:4]) != "OggS" {
		got := downloaded
		if len(got) > 4 {
			got = got[:4]
		}
		t.Fatalf("got downloaded media starting with %q, want the Ogg magic bytes \"OggS\" — Meta did not store/serve back a genuine Ogg container", got)
	}
	if !strings.Contains(downloadedMimeType, "ogg") {
		t.Fatalf("got downloaded mime type %q, want it to mention ogg", downloadedMimeType)
	}
}
