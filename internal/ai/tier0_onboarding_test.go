package ai_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

// TestProcessor_EditOnOverpayment_DoesNotRetriggerOnboarding is docs/
// BRIEF-disambiguation-reminders-statements.md Tier 0's required
// regression test: a real (not dbtest.CreateUser-shortcut — that always
// pre-sets a name, which is why this interaction was never exercised
// end to end) onboarded trader creates a debt, attempts an overpayment,
// taps Edit, then sends two completely unrelated messages. Root-cause
// investigation (full trace of every users.name writer, direct
// reproduction against real Postgres/Redis, and the phone_number UNIQUE
// constraint ruling out a duplicate-account race) found no code path
// where Edit's handler — which only clears PendingConfirm in Redis and
// never touches the account row — can cause an already-named user to be
// treated as unnamed. This asserts that finding stays true: the named
// user's users.name and general capability (Help, then a real query)
// are unaffected by tapping Edit on an overpayment confirmation.
func TestProcessor_EditOnOverpayment_DoesNotRetriggerOnboarding(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)

	// resolveOrCreateUser's real path (account.Create): a brand-new
	// phone number gets an empty-name account, exactly like production.
	var userID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (name, phone_number) VALUES ('', $1) RETURNING id`, "+2348170000001",
	).Scan(&userID); err != nil {
		t.Fatalf("seed blank user: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentRecordPayment, CustomerName: new("Chinedu"), AmountMinor: new(int64(10000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentHelp, Language: ai.LangEnglish},
		{Intent: ai.IntentListOutstandingDebts, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	// Real onboarding — bare greeting, then the name.
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0.1", "text", new("Hi"))); err != nil {
		t.Fatalf("first contact: %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0.2", "text", new("Kingsley"))); err != nil {
		t.Fatalf("name capture: %v", err)
	}

	// Create a debt, then trigger an overpayment.
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0.3", "text", new("Chinedu took 75k"))); err != nil {
		t.Fatalf("create debt: %v", err)
	}
	overpay, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0.4", "text", new("Chinedu paid 100k")))
	if err != nil {
		t.Fatalf("overpayment attempt: %v", err)
	}
	if len(overpay.Buttons) != 3 {
		t.Fatalf("got %d buttons on the overpayment prompt, want 3 (Confirm/Edit/Cancel), got %+v", len(overpay.Buttons), overpay)
	}

	// Tap Edit.
	edit, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0.5", "interactive", new("edit")))
	if err != nil {
		t.Fatalf("tap edit: %v", err)
	}
	if strings.Contains(edit.Text, "what should I call you") {
		t.Fatalf("got %q for the Edit tap itself, want the edit prompt, not first-contact onboarding", edit.Text)
	}

	var nameAfterEdit string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM users WHERE id = $1`, userID).Scan(&nameAfterEdit); err != nil {
		t.Fatalf("query name after edit: %v", err)
	}
	if nameAfterEdit != "Kingsley" {
		t.Fatalf("got users.name = %q after tapping Edit, want it unchanged (\"Kingsley\")", nameAfterEdit)
	}

	// Two unrelated messages afterward must be handled normally, never
	// re-onboarded.
	help, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0.6", "text", new("Help")))
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if strings.Contains(help.Text, "what should I call you") || strings.Contains(help.Text, "I just need your name") {
		t.Fatalf("got %q for an unrelated Help message after Edit, want the real HELP response, not onboarding", help.Text)
	}
	if !strings.Contains(help.Text, "Record a sale on credit") {
		t.Fatalf("got %q, want the real capability list", help.Text)
	}

	balance, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0.7", "text", new("Who owes me?")))
	if err != nil {
		t.Fatalf("who owes me: %v", err)
	}
	if strings.Contains(balance.Text, "what should I call you") || strings.Contains(balance.Text, "I just need your name") {
		t.Fatalf("got %q for an unrelated query after Edit, want the real answer, not onboarding", balance.Text)
	}
	if !strings.Contains(balance.Text, "Chinedu") {
		t.Fatalf("got %q, want the real outstanding-debts answer naming Chinedu", balance.Text)
	}

	var finalName string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM users WHERE id = $1`, userID).Scan(&finalName); err != nil {
		t.Fatalf("query final name: %v", err)
	}
	if finalName != "Kingsley" {
		t.Fatalf("got final users.name = %q, want it still \"Kingsley\" after the whole sequence", finalName)
	}
}

// TestProcessor_NameCapture_SelfHealsAcrossStrayInteractiveTaps confirms
// the reask/placeholder bound: even a sequence of stray interactive
// taps (e.g. leftover quick-action buttons still visible on a trader's
// screen) during name capture terminates within the designed 2-message
// bound rather than looping — the other angle Tier 0 asked to be
// verified, not just the Edit-specific one.
func TestProcessor_NameCapture_SelfHealsAcrossStrayInteractiveTaps(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	var userID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (name, phone_number) VALUES ('', $1) RETURNING id`, "+2348170000002",
	).Scan(&userID); err != nil {
		t.Fatalf("seed blank user: %v", err)
	}

	p := newTestProcessor(pool, rdb, &fakeExtractor{}, &fakePhraser{}, &fakeSender{})

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0b.1", "interactive", new("menu:help"))); err != nil {
		t.Fatalf("1: %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0b.2", "interactive", new("menu:balance"))); err != nil {
		t.Fatalf("2: %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t0b.3", "interactive", new("menu:help"))); err != nil {
		t.Fatalf("3: %v", err)
	}

	var name string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM users WHERE id = $1`, userID).Scan(&name); err != nil {
		t.Fatalf("query name: %v", err)
	}
	if name == "" {
		t.Fatal("got empty users.name after 3 messages, want the placeholder to have been accepted by now")
	}
}
