package ai_test

import (
	"context"
	"sync"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/money"
	"github.com/chibuike-kt/ruby/internal/payment"
)

// fakeSpeaker is docs/BRIEF-research-hardening-standard.md Part 5 Tier
// 1's voice-reply double: it records every text it was asked to
// synthesize and returns fixed, recognizable audio bytes so a test can
// prove the exact reply text reached it (never raw trader text — the
// same boundary Phraser already keeps).
type fakeSpeaker struct {
	mu    sync.Mutex
	texts []string
}

func (f *fakeSpeaker) Speak(_ context.Context, text string) ([]byte, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.texts = append(f.texts, text)
	return []byte("synthesized:" + text), "audio/aac", nil
}

func (f *fakeSpeaker) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.texts) == 0 {
		return ""
	}
	return f.texts[len(f.texts)-1]
}

// TestProcessor_VoiceReply_SentForAudioOriginatedPlainTextReply is Part 5
// Tier 1's minimum bar: a voice-note-originated message whose reply is
// plain text (no buttons/list) gets a synthesized audio reply alongside
// the usual text send.
func TestProcessor_VoiceReply_SentForAudioOriginatedPlainTextReply(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348200000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentGetTotalOutstanding, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	speaker := &fakeSpeaker{}
	p := ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{text: "how much am I owed in total"},
		Transcoder:  fakeTranscoder{},
		Phraser:     phraser,
		Sender:      sender,
		Media:       fakeMedia{data: []byte("ogg-bytes")},
		Speaker:     speaker,
		Pool:        pool,
		Redis:       rdb,
		Customers:   customer.NewService(pool),
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
	})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.voicereply.1", "audio", new("media-id-1")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) != 0 {
		t.Fatalf("got %d buttons, want a plain-text reply for this test setup", len(reply.Buttons))
	}

	if got := speaker.last(); got != reply.Text {
		t.Fatalf("got synthesized text %q, want it to match the actual reply text %q", got, reply.Text)
	}
	audio := sender.lastAudio()
	if string(audio.audio) != "synthesized:"+reply.Text {
		t.Fatalf("got sent audio %q, want the synthesized bytes to reach SendAudio", audio.audio)
	}
	if audio.mimeType != "audio/aac" {
		t.Fatalf("got mime type %q, want audio/aac", audio.mimeType)
	}
	if sender.last().body != reply.Text {
		t.Fatalf("got sent text %q, want the same reply.Text %q — voice is additive, never a replacement", sender.last().body, reply.Text)
	}
}

// TestProcessor_VoiceReply_NotSentForTextOriginatedMessage confirms the
// feature is scoped to voice-note-originated messages, not every reply.
func TestProcessor_VoiceReply_NotSentForTextOriginatedMessage(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348200000002")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentGetTotalOutstanding, Language: ai.LangEnglish},
	}}
	speaker := &fakeSpeaker{}
	p := ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{},
		Transcoder:  fakeTranscoder{},
		Phraser:     &fakePhraser{},
		Sender:      &fakeSender{},
		Media:       fakeMedia{},
		Speaker:     speaker,
		Pool:        pool,
		Redis:       rdb,
		Customers:   customer.NewService(pool),
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
	})

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.voicereply.2", "text", new("how much am I owed in total"))); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := speaker.last(); got != "" {
		t.Fatalf("got synthesized text %q, want no voice reply for a text-originated message", got)
	}
}

// TestProcessor_VoiceReply_SkippedWhenReplyHasButtons confirms a reply
// carrying buttons/a list never also gets a redundant voice-only
// version — the trader still needs to see and tap it regardless.
func TestProcessor_VoiceReply_SkippedWhenReplyHasButtons(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348200000003")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
	}}
	speaker := &fakeSpeaker{}
	p := ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{text: "Chinedu took 75k"},
		Transcoder:  fakeTranscoder{},
		Phraser:     &fakePhraser{},
		Sender:      &fakeSender{},
		Media:       fakeMedia{data: []byte("ogg-bytes")},
		Speaker:     speaker,
		Pool:        pool,
		Redis:       rdb,
		Customers:   customer.NewService(pool),
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
	})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.voicereply.3", "audio", new("media-id-1")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) == 0 {
		t.Fatalf("got no buttons, want a confirmation prompt (this test needs a buttons-carrying reply)")
	}
	if got := speaker.last(); got != "" {
		t.Fatalf("got synthesized text %q, want no voice reply when the text reply carries buttons", got)
	}
}

// TestProcessor_VoiceReply_SlotFillQuestionFromVoice_CompletionByTextGetsNone
// is a named regression case: a slot-fill flow started by a voice
// message must get a voice reply for the *question itself* (proving the
// centralized Handle()-level dispatch reaches handleSlotFillReply's
// return path exactly like any other, not just the initial-extraction
// branch), while a later reply produced by a *typed* follow-up must not
// — the channel matches each individual message, never a sticky
// "this conversation started as voice" flag.
func TestProcessor_VoiceReply_SlotFillQuestionFromVoice_CompletionByTextGetsNone(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348200000004")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // missing both
	}}
	sender := &fakeSender{}
	speaker := &fakeSpeaker{}
	p := ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{text: "someone took goods"},
		Transcoder:  fakeTranscoder{},
		Phraser:     &fakePhraser{},
		Sender:      sender,
		Media:       fakeMedia{data: []byte("ogg-bytes")},
		Speaker:     speaker,
		Pool:        pool,
		Redis:       rdb,
		Customers:   customer.NewService(pool),
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
	})

	question, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.voicereply.4a", "audio", new("media-id-1")))
	if err != nil {
		t.Fatalf("Handle (voice, missing both): %v", err)
	}
	if sender.last().body != question.Text {
		t.Fatalf("got sent text %q, want the slot-fill question %q — text must still send", sender.last().body, question.Text)
	}
	if speaker.last() != question.Text {
		t.Fatalf("got synthesized text %q, want the slot-fill question %q — a voice-originated slot-fill question must also get a voice reply", speaker.last(), question.Text)
	}
	callsAfterQuestion := len(speaker.texts)

	answer, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.voicereply.4b", "text", new("Chinedu")))
	if err != nil {
		t.Fatalf("Handle (typed follow-up): %v", err)
	}
	if sender.last().body != answer.Text {
		t.Fatalf("got sent text %q, want the next slot-fill question %q — text must still send for the typed turn too", sender.last().body, answer.Text)
	}
	if len(speaker.texts) != callsAfterQuestion {
		t.Fatalf("got %d voice syntheses after a typed follow-up, want still %d — a text-originated turn must never get a voice reply, even mid-flow", len(speaker.texts), callsAfterQuestion)
	}
}

// TestProcessor_VoiceReply_DeterministicListReply_GetsVoiceToo is the
// other named regression case: LIST_OUTSTANDING_DEBTS ("who owes me")
// is built entirely in Go (format.go), never through the Phraser — the
// voice dispatch must key off reply.Text generically, not "was this
// reply produced by a Phraser call," or a spoken query for a
// deterministically-formatted reply would silently never get voice.
func TestProcessor_VoiceReply_DeterministicListReply_GetsVoiceToo(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348200000005")
	customers := customer.NewService(pool)
	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := debt.NewService(pool).Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentListOutstandingDebts, Language: ai.LangEnglish},
	}}
	sender := &fakeSender{}
	speaker := &fakeSpeaker{}
	p := ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{text: "who owes me"},
		Transcoder:  fakeTranscoder{},
		Phraser:     &fakePhraser{}, // must never be called: this path is deterministic
		Sender:      sender,
		Media:       fakeMedia{data: []byte("ogg-bytes")},
		Speaker:     speaker,
		Pool:        pool,
		Redis:       rdb,
		Customers:   customers,
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
	})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.voicereply.5", "audio", new("media-id-1")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if sender.last().body != reply.Text {
		t.Fatalf("got sent text %q, want the deterministic list %q — text must still send", sender.last().body, reply.Text)
	}
	if speaker.last() != reply.Text {
		t.Fatalf("got synthesized text %q, want the same deterministic list %q — a spoken query answered deterministically must still get a voice reply", speaker.last(), reply.Text)
	}
}
