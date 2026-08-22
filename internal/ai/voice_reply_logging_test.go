package ai_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/payment"
)

// capturingHandler is a minimal slog.Handler double so a test can assert
// on the level and attributes of a specific log line, rather than just
// that "something" was logged.
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}

func (h *capturingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(string) slog.Handler      { return h }

func (h *capturingHandler) attr(r slog.Record, key string) string {
	var val string
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			val = a.Value.String()
			return false
		}
		return true
	})
	return val
}

func (h *capturingHandler) find(level slog.Level, msgSubstr string) (slog.Record, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Level == level && strings.Contains(r.Message, msgSubstr) {
			return r, true
		}
	}
	return slog.Record{}, false
}

// fakeFailingSpeaker always errors — a stand-in for a real OpenAI TTS
// call failing in production.
type fakeFailingSpeaker struct{ err error }

func (f fakeFailingSpeaker) Speak(context.Context, string) ([]byte, string, error) {
	return nil, "", f.err
}

// fakeSenderFailingAudio sends text normally but always fails SendAudio
// — a stand-in for the real WhatsApp audio-send call failing in
// production while everything else works.
type fakeSenderFailingAudio struct {
	*fakeSender
	err error
}

func (f *fakeSenderFailingAudio) SendAudio(context.Context, string, []byte, string) error {
	return f.err
}

// TestProcessor_VoiceReply_SynthesisFailure_LogsErrorWithStage is docs/
// BRIEF-research-hardening-standard.md Part 5 live-testing finding #3:
// a mocked test suite can't catch a real integration failure, but it
// can at least prove the diagnostics meant to catch one are wired
// correctly — Error level (not Warn), tagged with which of the two
// external calls (synthesize vs send) actually failed.
func TestProcessor_VoiceReply_SynthesisFailure_LogsErrorWithStage(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348240000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentGetTotalOutstanding, Language: ai.LangEnglish},
	}}
	handler := &capturingHandler{}
	p := ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{text: "how much am I owed"},
		Transcoder:  fakeTranscoder{},
		Phraser:     &fakePhraser{},
		Sender:      &fakeSender{},
		Media:       fakeMedia{data: []byte("ogg-bytes")},
		Speaker:     fakeFailingSpeaker{err: errors.New("openai: request failed with status 500")},
		Pool:        pool,
		Redis:       rdb,
		Customers:   customer.NewService(pool),
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
		Logger:      slog.New(handler),
	})

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.voicelog.1", "audio", new("media-id-1"))); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rec, ok := handler.find(slog.LevelError, "voice reply failed")
	if !ok {
		t.Fatal("got no Error-level \"voice reply failed\" log line for a synthesis failure")
	}
	if stage := handler.attr(rec, "stage"); stage != "synthesize" {
		t.Fatalf("got stage %q, want \"synthesize\"", stage)
	}
}

// TestProcessor_VoiceReply_SendFailure_LogsErrorWithStage is the send-
// side counterpart: synthesis succeeds but the real WhatsApp audio-send
// call fails.
func TestProcessor_VoiceReply_SendFailure_LogsErrorWithStage(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348240000002")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentGetTotalOutstanding, Language: ai.LangEnglish},
	}}
	handler := &capturingHandler{}
	sender := &fakeSenderFailingAudio{fakeSender: &fakeSender{}, err: errors.New("whatsapp: media upload failed with status 400")}
	p := ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{text: "how much am I owed"},
		Transcoder:  fakeTranscoder{},
		Phraser:     &fakePhraser{},
		Sender:      sender,
		Media:       fakeMedia{data: []byte("ogg-bytes")},
		Speaker:     &fakeSpeaker{},
		Pool:        pool,
		Redis:       rdb,
		Customers:   customer.NewService(pool),
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
		Logger:      slog.New(handler),
	})

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.voicelog.2", "audio", new("media-id-1"))); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rec, ok := handler.find(slog.LevelError, "voice reply failed")
	if !ok {
		t.Fatal("got no Error-level \"voice reply failed\" log line for a send failure")
	}
	if stage := handler.attr(rec, "stage"); stage != "send" {
		t.Fatalf("got stage %q, want \"send\"", stage)
	}
}
