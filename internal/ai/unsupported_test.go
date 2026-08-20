package ai_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

// TestProcessor_Unsupported_HonestDecline is docs/BRIEF-critical-fixes-
// and-reminders.md #1c's core requirement: a genuinely unsupported
// request gets an explicit, honest decline plus the real capability
// list — never the reminder-specific "noted, check back soon" text
// (that was the pre-fix bug: UnsupportedAction always showed
// reminderUnsupportedText regardless of which unsupported intent it
// was), and never a mutation attempt.
func TestProcessor_Unsupported_HonestDecline(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348110100001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentUnsupported, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.unsupported.1", "text", new("send Chinedu an invoice for 2 bags of rice")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "can't do that yet") {
		t.Fatalf("got reply %q, want the honest-decline framing", reply.Text)
	}
	if strings.Contains(reply.Text, "noted your request") {
		t.Fatalf("got reply %q, want the general decline text, not the reminder-specific one", reply.Text)
	}
	// Same underlying capability list as HELP, so the decline is
	// concrete, not a vague "I can't help."
	for _, want := range []string{"Record a debt", "Record a payment", "who owes"} {
		if !strings.Contains(reply.Text, want) {
			t.Fatalf("got reply %q, want it to include the capability list (e.g. %q)", reply.Text, want)
		}
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls for an unsupported request, want 0 — it's a fixed response", len(phraser.inputs))
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 0 {
		t.Fatalf("got %d debts created from an unsupported request, want 0", got)
	}

	if _, ok, err := ai.GetPendingAction(context.Background(), rdb, userID); err != nil || ok {
		t.Fatalf("got a pending action set after an unsupported request (ok=%v err=%v), want none — no improvised flow", ok, err)
	}
}

// TestProcessor_Unsupported_DistinctFromReminderIntents confirms
// CREATE_REMINDER/CANCEL_REMINDER keep their own dedicated response —
// 1c's fix must not collapse that distinction.
func TestProcessor_Unsupported_DistinctFromReminderIntents(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348110100002")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateReminder, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.unsupported.2", "text", new("remind me to follow up with Chinedu next week")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "noted your request") {
		t.Fatalf("got reply %q, want the reminder-specific text (unchanged)", reply.Text)
	}
}
