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
	"github.com/chibuike-kt/ruby/internal/payment"
)

// TestProcessor_CustomerStatement_ShowsDebtsPaymentsAndOutstanding is
// docs/BRIEF-disambiguation-reminders-statements.md Tier 3's core
// requirement: every debt with item description and date, every
// payment against each, and the current outstanding balance, built
// deterministically (never a Phraser call — this is real financial
// data being read back).
func TestProcessor_CustomerStatement_ShowsDebtsPaymentsAndOutstanding(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348190200001")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)
	payments := payment.NewService(pool)

	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	d1, err := debts.Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "2 cartons of noodles", nil)
	if err != nil {
		t.Fatalf("seed debt 1: %v", err)
	}
	if _, err := payments.Record(context.Background(), userID, d1.ID, money.New(5000000, money.NGN), "stmt-key-1"); err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c.ID, money.New(1200000, money.NGN), "1 bag of rice", nil); err != nil {
		t.Fatalf("seed debt 2: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentGetCustomerStatement, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t3.1", "text", new("give me a breakdown for Chinedu")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls, want 0 — the statement is built deterministically", len(phraser.inputs))
	}

	for _, want := range []string{
		"2 cartons of noodles", "75,000",
		"1 bag of rice", "12,000",
		"50,000", // the payment
	} {
		if !strings.Contains(reply.Text, want) {
			t.Fatalf("got statement %q, want it to contain %q", reply.Text, want)
		}
	}
	// Outstanding: (75,000 - 50,000) + 12,000 = 37,000.
	if !strings.Contains(reply.Text, "37,000") {
		t.Fatalf("got statement %q, want the outstanding total 37,000", reply.Text)
	}
}

// TestProcessor_CustomerStatement_IncludesSettledDebts confirms this is
// a real account history, not just what's currently outstanding — a
// fully-paid debt still shows up with its payment, just contributing
// nothing to the outstanding total.
func TestProcessor_CustomerStatement_IncludesSettledDebts(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348190200002")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)
	payments := payment.NewService(pool)

	c, err := customers.Create(context.Background(), userID, "Ngozi", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	d, err := debts.Create(context.Background(), userID, c.ID, money.New(500000, money.NGN), "airtime", nil)
	if err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	if _, err := payments.Record(context.Background(), userID, d.ID, money.New(500000, money.NGN), "stmt-key-2"); err != nil {
		t.Fatalf("seed full payment: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentGetCustomerStatement, CustomerName: new("Ngozi"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t3b.1", "text", new("summarize what Ngozi owes")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "airtime") {
		t.Fatalf("got statement %q, want the settled debt still listed", reply.Text)
	}
	if !strings.Contains(reply.Text, "5,000") {
		t.Fatalf("got statement %q, want the debt amount", reply.Text)
	}
	if !strings.Contains(reply.Text, "Outstanding") {
		t.Fatalf("got statement %q, want an outstanding total line", reply.Text)
	}
	// Outstanding must be 0 — the debt is fully settled.
	idx := strings.Index(reply.Text, "Outstanding")
	if !strings.Contains(reply.Text[idx:], "0") {
		t.Fatalf("got statement %q, want outstanding to read 0 for a fully-settled debt", reply.Text)
	}
}

// TestProcessor_CustomerStatement_NoDebts_EmptyState confirms a
// customer with no debts on record gets a genuine empty-state message,
// not an empty list or a crash.
func TestProcessor_CustomerStatement_NoDebts_EmptyState(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348190200003")
	customers := customer.NewService(pool)

	if _, err := customers.Create(context.Background(), userID, "Ada", nil, nil); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentGetCustomerStatement, CustomerName: new("Ada"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t3c.1", "text", new("show Ada's account")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "Ada") {
		t.Fatalf("got reply %q, want it to name Ada", reply.Text)
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls for the empty state, want 0", len(phraser.inputs))
	}
}
