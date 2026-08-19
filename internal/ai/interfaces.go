package ai

import "context"

// Extractor turns already-transcribed (or originally-text) trader
// language into a RawIntent — one Structured Outputs call, strict mode,
// covering intent + entities + language detection together (per the
// brief: detection is part of the same call, not a separate round trip).
type Extractor interface {
	Extract(ctx context.Context, text string) (RawIntent, error)
}

// Transcriber turns already-transcoded audio into text. langHints are
// passed through to the provider's own language-hint mechanism —
// Processor always offers all five supported languages, never narrows
// based on a guess.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, langHints []Language) (transcript string, detectedLanguage Language, err error)
}

// Transcoder converts a WhatsApp voice note (.ogg/Opus) into a format
// gpt-transcribe accepts (mp3/wav/...) — the format mismatch
// docs/BRIEF-ai-intent.md calls out explicitly.
type Transcoder interface {
	Transcode(ctx context.Context, ogg []byte) (transcoded []byte, err error)
}

// Phraser is the second, narrowly-scoped AI call (plan decision #7): it
// receives only an already-decided PhraseInput, never raw trader text,
// and returns only phrasing in the target language — terminal output,
// never parsed back into anything financial.
type Phraser interface {
	Phrase(ctx context.Context, input PhraseInput) (string, error)
}

// Sender dispatches Ruby's reply back over WhatsApp. Satisfied by
// *whatsapp.Service; ai never imports whatsapp — see plan decision #10.
type Sender interface {
	SendText(ctx context.Context, to, body string) error
}

// MediaDownloader fetches the bytes behind a WhatsApp media id (spec
// §22's "WhatsApp Media API" step). Satisfied by *whatsapp.Service.
type MediaDownloader interface {
	DownloadMedia(ctx context.Context, mediaID string) (data []byte, mimeType string, err error)
}
