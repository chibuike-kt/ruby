package ai_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/money"
)

// TestProcessor_ReturningTrader_GreetedByNameNeverAskedAgain is the
// required test: an existing user with a stored name (dbtest.CreateUser
// always sets one) gets the short "Hello {name}!" greeting, never the
// full first-contact intro, and is never asked for their name.
func TestProcessor_ReturningTrader_GreetedByNameNeverAskedAgain(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348091000001") // name: "Test Trader"

	extractor := &fakeExtractor{}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.returning.1", "text", new("hi")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "Test Trader") {
		t.Fatalf("got reply %q, want the trader greeted by their stored name", reply.Text)
	}
	if strings.Contains(reply.Text, "what should I call you") || strings.Contains(reply.Text, "First —") {
		t.Fatalf("got reply %q, want no name question — this trader already has a name", reply.Text)
	}
	if strings.Contains(reply.Text, "bookkeeping assistant") {
		t.Fatalf("got reply %q, want the short returning greeting, not the full first-contact intro/pitch", reply.Text)
	}
	if len(extractor.calls) != 0 {
		t.Fatalf("got %d extractor calls for a bare greeting, want 0", len(extractor.calls))
	}
}

func TestProcessor_Help_ContainsConcreteExamplePhrasings(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348091000002")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentHelp, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.help.1", "text", new("what can you do")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls for HELP, want 0 — it's a fixed response", len(phraser.inputs))
	}

	// Concrete example phrasings, not just intent names/feature labels.
	for _, want := range []string{"took", "paid", "who owes", "5k"} {
		if !strings.Contains(reply.Text, want) {
			t.Fatalf("got HELP reply %q, want it to contain an example phrasing like %q", reply.Text, want)
		}
	}
	if !strings.Contains(reply.Text, "*") {
		t.Fatalf("got HELP reply %q, want WhatsApp bold formatting applied", reply.Text)
	}
}

func TestProcessor_ListOutstandingDebts_EmptyState(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348091000003")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentListOutstandingDebts, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.list.empty.1", "text", new("who owes me?")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls for an empty list, want 0 — fixed empty-state text", len(phraser.inputs))
	}
	if reply.Text == "" {
		t.Fatal("got an empty reply for the empty-debts case, want the empty-state message")
	}
	if !strings.Contains(strings.ToLower(reply.Text), "clear") && !strings.Contains(strings.ToLower(reply.Text), "no one owes") {
		t.Fatalf("got reply %q, want a plain, warm empty-state message", reply.Text)
	}
}

// TestProcessor_ListOutstandingDebts_MultipleDebtors_Formatted is the
// required test: multiple debtors produce one formatted entry each,
// plus a total line — and never touch the Phraser (docs/BRIEF-response-quality.md
// #4/#5: this is built deterministically to protect the grounding
// backstop).
func TestProcessor_ListOutstandingDebts_MultipleDebtors_Formatted(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348091000004")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	c1, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer 1: %v", err)
	}
	c2, err := customers.Create(context.Background(), userID, "Ngozi", nil, nil)
	if err != nil {
		t.Fatalf("seed customer 2: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c1.ID, money.New(7500000, money.NGN), "noodles", nil); err != nil {
		t.Fatalf("seed debt 1: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c2.ID, money.New(1200000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt 2: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentListOutstandingDebts, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.list.multi.1", "text", new("who owes me?")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls, want 0 — the list is built deterministically, never AI-phrased", len(phraser.inputs))
	}

	for _, want := range []string{"*Chinedu*", "*Ngozi*", "75,000", "12,000"} {
		if !strings.Contains(reply.Text, want) {
			t.Fatalf("got reply %q, want it to contain %q", reply.Text, want)
		}
	}
	// Total outstanding across all customers as a closing line.
	if !strings.Contains(reply.Text, "87,000") {
		t.Fatalf("got reply %q, want a total-outstanding closing line (75,000 + 12,000 = 87,000)", reply.Text)
	}
	// Blank line between entries.
	if !strings.Contains(reply.Text, "\n\n") {
		t.Fatalf("got reply %q, want blank lines separating entries for readability", reply.Text)
	}
}
