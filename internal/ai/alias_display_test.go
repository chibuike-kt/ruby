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

// TestProcessor_CreateDebt_EchoesAlias is docs/BRIEF-research-hardening-
// standard.md Part 5 live-testing finding #2: a customer's alias must
// be echoed back at the moment it matters — here, the debt-created
// confirmation naming who it's actually for — not left silently
// unconfirmed.
func TestProcessor_CreateDebt_EchoesAlias(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348230000001")
	customers := customer.NewService(pool)
	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, new("Atlas"))
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := customer.SetLastCustomerContext(context.Background(), rdb, userID, c.ID, customer.DefaultLastCustomerContextTTL); err != nil {
		t.Fatalf("seed last customer context: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	p := newTestProcessor(pool, rdb, extractor, phraser, &fakeSender{})

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.alias.1", "text", new("Chinedu took 75k"))); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if phraser.last().CustomerName != "Chinedu (Atlas)" {
		t.Fatalf("got customer name %q, want %q — the alias must be echoed in the debt-created confirmation", phraser.last().CustomerName, "Chinedu (Atlas)")
	}
}

// TestProcessor_ListCustomers_ShowsAlias covers the audit docs/BRIEF-
// research-hardening-standard.md Part 5 live-testing finding #2 asked
// for: does an alias actually surface in a plain customer list, or do
// same-named customers just print identically?
func TestProcessor_ListCustomers_ShowsAlias(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348230000002")
	customers := customer.NewService(pool)
	if _, err := customers.Create(context.Background(), userID, "Chinedu", nil, new("Atlas")); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentListCustomers, Language: ai.LangEnglish},
	}}
	p := newTestProcessor(pool, rdb, extractor, &fakePhraser{}, &fakeSender{})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.alias.2", "text", new("list my customers")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "Chinedu (Atlas)") {
		t.Fatalf("got reply %q, want it to show Chinedu (Atlas)", reply.Text)
	}
}

// TestProcessor_ListOutstandingDebts_DistinguishesSameNamedCustomers is
// the other half of the audit: two different customers sharing a name
// must not appear as two identically-labeled groups in the outstanding
// list.
func TestProcessor_ListOutstandingDebts_DistinguishesSameNamedCustomers(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348230000003")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)
	c1, err := customers.Create(context.Background(), userID, "Chinedu", nil, new("Atlas"))
	if err != nil {
		t.Fatalf("seed customer 1: %v", err)
	}
	c2, err := customers.Create(context.Background(), userID, "Chinedu", nil, new("Mechanic"))
	if err != nil {
		t.Fatalf("seed customer 2: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c1.ID, money.New(5000000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt 1: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c2.ID, money.New(3000000, money.NGN), "beans", nil); err != nil {
		t.Fatalf("seed debt 2: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentListOutstandingDebts, Language: ai.LangEnglish},
	}}
	p := newTestProcessor(pool, rdb, extractor, &fakePhraser{}, &fakeSender{})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.alias.3", "text", new("who owes me")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "Chinedu (Atlas)") || !strings.Contains(reply.Text, "Chinedu (Mechanic)") {
		t.Fatalf("got reply %q, want both Chinedu (Atlas) and Chinedu (Mechanic) — the two same-named customers must be distinguishable", reply.Text)
	}
}

// TestProcessor_Disambiguation_LabelsHintKind is the exact live-testing
// scenario from finding #2: one candidate distinguished by alias, the
// other by last-item description — both must be explicitly labeled, so
// "Atlas" can never read as something purchased instead of a person's
// nickname.
func TestProcessor_Disambiguation_LabelsHintKind(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348230000004")
	debts := debt.NewService(pool)
	alias := "Atlas"
	// customer.Create (repository-level) bypasses Service.Create's
	// duplicate-name creation guard — needed here only to construct a
	// same-named-with-no-alias-or-phone candidate as test fixture data;
	// the creation guard itself isn't what this test is about.
	c1, err := customer.Create(context.Background(), pool, customer.Customer{UserID: userID, Name: "Chinedu", Alias: &alias})
	if err != nil {
		t.Fatalf("seed customer 1: %v", err)
	}
	c2, err := customer.Create(context.Background(), pool, customer.Customer{UserID: userID, Name: "Chinedu"})
	if err != nil {
		t.Fatalf("seed customer 2: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c1.ID, money.New(5000000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt 1: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c2.ID, money.New(3000000, money.NGN), "two cartons of noodles", nil); err != nil {
		t.Fatalf("seed debt 2: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentRecordPayment, CustomerName: new("Chinedu"), AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	p := newTestProcessor(pool, rdb, extractor, phraser, &fakeSender{})

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.alias.4", "text", new("Chinedu paid me 5k"))); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	items := phraser.last().Items
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	joined := strings.Join(items, " | ")
	if !strings.Contains(joined, "alias: Atlas") {
		t.Fatalf("got items %v, want one labeled \"alias: Atlas\"", items)
	}
	if !strings.Contains(joined, "last item: two cartons of noodles") {
		t.Fatalf("got items %v, want one labeled \"last item: two cartons of noodles\"", items)
	}
}
