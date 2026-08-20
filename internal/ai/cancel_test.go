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

// TestProcessor_Cancel_ExitsDisambiguation and
// TestProcessor_Cancel_ExitsCustomerSignal are the required proof that
// "never mind"/"cancel" exits *any* pending state, not just one flow
// (docs/BRIEF-critical-fixes-and-reminders.md's slot-filling edge case
// #4) — two structurally different pending kinds, same cancel path.
func TestProcessor_Cancel_ExitsDisambiguation(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348150000001")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	c1, err := customers.Create(context.Background(), userID, "Chinedu", new("+2348030000021"), nil)
	if err != nil {
		t.Fatalf("seed customer 1: %v", err)
	}
	c2, err := customers.Create(context.Background(), userID, "Chinedu", new("+2348030000022"), nil)
	if err != nil {
		t.Fatalf("seed customer 2: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c1.ID, money.New(1000000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt 1: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c2.ID, money.New(2000000, money.NGN), "beans", nil); err != nil {
		t.Fatalf("seed debt 2: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentRecordPayment, CustomerName: new("Chinedu"), AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.cancel.disambig.1", "text", new("Chinedu paid me 5k"))); err != nil {
		t.Fatalf("Handle (trigger disambiguation): %v", err)
	}

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.cancel.disambig.2", "text", new("never mind")))
	if err != nil {
		t.Fatalf("Handle (cancel): %v", err)
	}
	if !strings.Contains(strings.ToLower(reply.Text), "cancel") {
		t.Fatalf("got reply %q, want a cancellation acknowledgment", reply.Text)
	}
	if _, ok, err := ai.GetPendingAction(context.Background(), rdb, userID); err != nil || ok {
		t.Fatalf("got a pending action still set after cancelling (ok=%v err=%v), want cleared", ok, err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 0 {
		t.Fatalf("got %d payments after cancelling, want 0", got)
	}
}

func TestProcessor_Cancel_ExitsCustomerSignalRequest(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348150000002")
	customers := customer.NewService(pool)
	if _, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.cancel.sig.1", "text", new("Chinedu took 50k"))); err != nil {
		t.Fatalf("Handle (trigger identity confirmation): %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.cancel.sig.2", "interactive", new("identity:new"))); err != nil {
		t.Fatalf("Handle (new person, ask for signal): %v", err)
	}

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.cancel.sig.3", "text", new("cancel")))
	if err != nil {
		t.Fatalf("Handle (cancel): %v", err)
	}
	if !strings.Contains(strings.ToLower(reply.Text), "cancel") {
		t.Fatalf("got reply %q, want a cancellation acknowledgment", reply.Text)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM customers WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d customers after cancelling, want 1 (no new one created)", got)
	}
}
