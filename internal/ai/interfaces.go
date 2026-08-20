package ai

import "context"

// Extractor turns already-transcribed (or originally-text) trader
// language into a RawIntent — one Structured Outputs call, strict mode,
// covering intent + entities + language detection together (per the
// brief: detection is part of the same call, not a separate round trip).
type Extractor interface {
	Extract(ctx context.Context, text string) (RawIntent, error)
}

// Transcriber turns already-transcoded audio into text. No language
// hint is passed to the provider (docs/BRIEF-fixes-and-reminders.md
// #2): gpt-transcribe's real API rejects any form of a multi-language
// hint field with a 400, confirmed against the live endpoint — every
// voice note was failing at this exact step. detectedLanguage comes
// from the transcription response itself, which already reports it
// without needing a hint.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte) (transcript string, detectedLanguage Language, err error)
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
// *whatsapp.Service; ai never imports whatsapp — see plan decision #10
// (whatsapp importing ai's Button/ListSection types to satisfy this is
// fine — it's one-directional and doesn't reintroduce that cycle).
// SendButtons/SendList are an enhancement over SendText, not a
// replacement (docs/BRIEF-interactive-messages.md) — Processor always
// has correct Reply.Text ready and only reaches for these when a reply
// actually has buttons/a list attached.
type Sender interface {
	SendText(ctx context.Context, to, body string) error
	SendButtons(ctx context.Context, to, body string, buttons []Button) error
	SendList(ctx context.Context, to, body, buttonLabel string, sections []ListSection) error
}

// MediaDownloader fetches the bytes behind a WhatsApp media id (spec
// §22's "WhatsApp Media API" step). Satisfied by *whatsapp.Service.
type MediaDownloader interface {
	DownloadMedia(ctx context.Context, mediaID string) (data []byte, mimeType string, err error)
}
