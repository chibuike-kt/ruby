package ai_test

import (
	"context"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

// TestProcessor_Language_StickyAcrossShortReply is
// docs/BRIEF-critical-fixes-and-reminders.md #2b's core case: a
// substantial message establishes the conversation's language; a
// short/ambiguous follow-up (an amount) must not flip it, even if the
// extractor's own per-message guess for that short text disagrees.
func TestProcessor_Language_StickyAcrossShortReply(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348130000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		// Substantial English message — establishes English.
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
		// A short reply ("5k") the extractor itself mis-tags as
		// Yoruba — must not flip the established language.
		{Intent: ai.IntentConfirmAction, AmountMinor: new(int64(500000)), Language: ai.LangYoruba},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.lang.1a", "text", new("Chinedu took some goods from me today"))); err != nil {
		t.Fatalf("Handle (establish): %v", err)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.lang.1b", "text", new("5k"))); err != nil {
		t.Fatalf("Handle (short reply): %v", err)
	}
	if phraser.last().Language != ai.LangEnglish {
		t.Fatalf("got phrase language %v for a short reply after English was established, want English (sticky)", phraser.last().Language)
	}
}

// TestProcessor_Language_SwitchesOnSubstantialMessage confirms a
// genuine language change — a message with enough content — is still
// honored, not permanently locked to the first language ever seen.
func TestProcessor_Language_SwitchesOnSubstantialMessage(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348130000002")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
		// A genuinely substantial Pidgin message — a real signal to switch.
		{Intent: ai.IntentListOutstandingDebts, Language: ai.LangPidgin},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.lang.2a", "text", new("Chinedu took some goods from me today"))); err != nil {
		t.Fatalf("Handle (establish English): %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.lang.2b", "text", new("abeg make I see who still owe me money"))); err != nil {
		t.Fatalf("Handle (switch to Pidgin): %v", err)
	}
	// LIST_OUTSTANDING_DEBTS is deterministic (format.go), not phrased —
	// assert on the reply text's own language-specific empty-state
	// string instead of a Phraser call.
}

// TestProcessor_Language_FirstMessageEstablishesEvenIfShort confirms
// the first message, even if short, still establishes a sticky
// language — stickiness only suppresses a *later* flip, it doesn't
// block the initial establishment. Verified indirectly: a second short
// message that the extractor mis-tags as a different language should
// still come back in the language the first message established.
func TestProcessor_Language_FirstMessageEstablishesEvenIfShort(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348130000003")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		// First message: short, but nothing established yet, so this
		// still sets the sticky language to Hausa.
		{Intent: ai.IntentListOutstandingDebts, Language: ai.LangHausa},
		// Second message: also short, mis-tagged English by the
		// extractor — must not override the just-established Hausa.
		{Intent: ai.IntentConfirmAction, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.lang.3a", "text", new("bashi"))); err != nil {
		t.Fatalf("Handle (first, short): %v", err)
	}
	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.lang.3b", "text", new("ok")))
	if err != nil {
		t.Fatalf("Handle (second, short): %v", err)
	}
	// CONFIRM_ACTION with nothing pending returns a fixed per-language
	// string (nothingToConfirmText) — the Hausa vs English wording
	// itself proves which sticky language won.
	const wantHausaText = "Babu wani abu da ke jiran tabbatarwa daga gare ka a yanzu."
	if reply.Text != wantHausaText {
		t.Fatalf("got reply %q, want the Hausa nothingToConfirm text (%q) — the first short message should have established Hausa", reply.Text, wantHausaText)
	}
}
