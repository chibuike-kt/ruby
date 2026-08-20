package ai_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

// TestProcessor_SmallTalk_BriefWarmReply is docs/BRIEF-critical-fixes-
// and-reminders.md #3a's small-talk path: genuine chit-chat gets a
// brief, warm reply — never the capability-list fallback (that's what
// this whole section replaces: SMALL_TALK used to collapse into the
// same catch-all as HELP/UNSUPPORTED).
func TestProcessor_SmallTalk_BriefWarmReply(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348140000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentSmallTalk, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.smalltalk.1", "text", new("how far, how you dey today")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if strings.Contains(reply.Text, "Record a debt") || strings.Contains(reply.Text, "can't do that yet") {
		t.Fatalf("got reply %q, want a brief small-talk reply, not the capability list or a decline", reply.Text)
	}
	if reply.Text == "" {
		t.Fatal("got an empty reply for small talk")
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls for small talk, want 0 — it's a fixed response", len(phraser.inputs))
	}
}

// TestProcessor_SelfQuery_AnswersFromStoredName is #3b's required case:
// "What is my name?" reads from users.name and answers directly.
func TestProcessor_SelfQuery_AnswersFromStoredName(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348140000002") // dbtest.CreateUser sets name "Test Trader"

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentSelfQuery, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.selfquery.1", "text", new("what is my name")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "Test Trader") {
		t.Fatalf("got reply %q, want it to state the stored name (Test Trader)", reply.Text)
	}
	if strings.Contains(reply.Text, "can't do that yet") || strings.Contains(reply.Text, "Record a debt") {
		t.Fatalf("got reply %q, want a direct answer, not a decline/capability list", reply.Text)
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls for a self-query, want 0 — it's answered directly from users.name", len(phraser.inputs))
	}
}
