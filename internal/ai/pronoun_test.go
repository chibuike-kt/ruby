package ai_test

import (
	"context"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

// TestProcessor_Pronoun_ResolvesToLastCustomer is docs/BRIEF-critical-
// fixes-and-reminders.md #2c's required direct test: name a customer,
// then reference them by pronoun in the next message, confirm
// resolution. Investigation found the whole chain (extraction leaving
// customer_name null for a pronoun, contextHint reading
// GetLastCustomerContext, customerRefFrom falling back to
// hint.LastCustomerID, and the identity-confirmation gate correctly
// bypassing itself for a hint-resolved reference) already works — this
// exists so that stays proven, not just observed once.
func TestProcessor_Pronoun_ResolvesToLastCustomer(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348999000200")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		// customer_name nil, exactly as the real extractor reports for
		// a pronoun reference — see spec §8 signal 4.
		{Intent: ai.IntentRecordPayment, CustomerName: nil, AmountMinor: new(int64(3000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.pronoun.1", "text", new("Chinedu took 50k"))); err != nil {
		t.Fatalf("Handle (name the customer): %v", err)
	}

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.pronoun.2", "text", new("he paid me 30k")))
	if err != nil {
		t.Fatalf("Handle (pronoun reference): %v", err)
	}
	if phraser.last().Event != ai.EventPaymentRecorded {
		t.Fatalf("got reply %q (event %v), want a successful EventPaymentRecorded phrase", reply.Text, phraser.last().Event)
	}

	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 1 {
		t.Fatalf("got %d payments, want 1 — the pronoun should have resolved to Chinedu via last-customer-context", got)
	}
}

// TestProcessor_Pronoun_ResolvesThroughLowConfidenceConfirmation covers
// the combination a pronoun reference plausibly triggers on its own —
// the model marking it low confidence (spec's own "genuinely unsure
// about a name" trigger) — which routes through a confirmation
// round-trip before resolution actually happens.
func TestProcessor_Pronoun_ResolvesThroughLowConfidenceConfirmation(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348999000201")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentRecordPayment, CustomerName: nil, AmountMinor: new(int64(3000000)), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
		{Intent: ai.IntentConfirmAction, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.pronoun.lc.1", "text", new("Chinedu took 50k"))); err != nil {
		t.Fatalf("Handle (name the customer): %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.pronoun.lc.2", "text", new("he paid me 30k"))); err != nil {
		t.Fatalf("Handle (pronoun, low confidence): %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.pronoun.lc.3", "text", new("yes"))); err != nil {
		t.Fatalf("Handle (confirm): %v", err)
	}

	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 1 {
		t.Fatalf("got %d payments, want 1 — pronoun resolution must survive the confirm round-trip", got)
	}
}

// TestProcessor_Pronoun_ResolvesForBalanceLookup covers the other
// common pronoun scenario: "how much does he owe me?"
func TestProcessor_Pronoun_ResolvesForBalanceLookup(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348999000202")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentGetCustomerBalance, CustomerName: nil, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.pronoun.bal.1", "text", new("Chinedu took 50k"))); err != nil {
		t.Fatalf("Handle (name the customer): %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.pronoun.bal.2", "text", new("how much does he owe me"))); err != nil {
		t.Fatalf("Handle (pronoun balance lookup): %v", err)
	}
	if phraser.last().Event != ai.EventCustomerBalance {
		t.Fatalf("got phrase event %v, want EventCustomerBalance", phraser.last().Event)
	}
	if phraser.last().CustomerName != "Chinedu" {
		t.Fatalf("got customer name %q in phrase input, want Chinedu — the pronoun should have resolved", phraser.last().CustomerName)
	}
}
