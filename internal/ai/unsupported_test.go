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

	// "generate a PDF report", not "send an invoice for Chinedu" —
	// docs/BRIEF-disambiguation-reminders-statements.md Tier 3 made a
	// per-customer statement/invoice a real, supported feature
	// (GET_CUSTOMER_STATEMENT); a downloadable file/report across all
	// customers is what's still genuinely unsupported.
	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.unsupported.1", "text", new("generate a PDF report of all my sales this month")))
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
	for _, want := range []string{"Record a sale on credit", "Log a payment", "Who owes"} {
		if !strings.Contains(reply.Text, want) {
			t.Fatalf("got reply %q, want it to include the capability list (e.g. %q)", reply.Text, want)
		}
	}
	if len(reply.Buttons) != 3 {
		t.Fatalf("got %d buttons on the unsupported-request decline, want 3 (docs/BRIEF-polish-and-hardening.md #3: the same greeting-menu quick actions, every time this content appears)", len(reply.Buttons))
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
// CREATE_REMINDER keeps its own dedicated handling, never collapsed
// into UNSUPPORTED's decline — docs/BRIEF-disambiguation-reminders-
// statements.md Tier 2a made CREATE_REMINDER a real, standalone
// feature (it used to reply "reminders aren't available yet" always;
// that placeholder is gone), so a message missing its required fields
// now goes through the same interactive slot-filling every other
// mutating intent gets, asking for what's missing rather than
// declining.
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
	if strings.Contains(reply.Text, "can't do that yet") || strings.Contains(reply.Text, "noted your request") {
		t.Fatalf("got reply %q, want CREATE_REMINDER handled as a real feature, not declined or stubbed", reply.Text)
	}
	if !strings.Contains(strings.ToLower(reply.Text), "who") {
		t.Fatalf("got reply %q, want the slot-fill customer question (no customer name was given)", reply.Text)
	}
}
