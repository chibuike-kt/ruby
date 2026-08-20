package ai_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

// TestProcessor_ListCustomers_EmptyState is docs/BRIEF-critical-fixes-
// and-reminders.md #2a's required test: no customers must produce a
// genuine, warm empty-state message — not a stub, not an empty/absent
// items list left for the model to improvise around.
func TestProcessor_ListCustomers_EmptyState(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348120000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentListCustomers, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.listcust.1", "text", new("who are my customers")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if reply.Text == "" {
		t.Fatal("got an empty reply for zero customers, want a genuine empty-state message")
	}
	if !strings.Contains(strings.ToLower(reply.Text), "haven't added") {
		t.Fatalf("got reply %q, want the empty-state message", reply.Text)
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls for LIST_CUSTOMERS, want 0 — it's deterministic now", len(phraser.inputs))
	}
}

// TestProcessor_ListCustomers_RealData is 2a's other half: the trader's
// actual customers, wired to real data, not a stub.
func TestProcessor_ListCustomers_RealData(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348120000002")
	customers := customer.NewService(pool)
	if _, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil); err != nil {
		t.Fatalf("seed customer 1: %v", err)
	}
	if _, err := customers.Create(context.Background(), userID, "Ngozi", nil, nil); err != nil {
		t.Fatalf("seed customer 2: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentListCustomers, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.listcust.2", "text", new("who are my customers")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "Chinedu") || !strings.Contains(reply.Text, "Ngozi") {
		t.Fatalf("got reply %q, want both real customer names", reply.Text)
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls for LIST_CUSTOMERS, want 0 — it's deterministic now", len(phraser.inputs))
	}
}
