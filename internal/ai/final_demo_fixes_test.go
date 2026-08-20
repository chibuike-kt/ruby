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

// TestProcessor_SlotFill_IdentityNeverAssumedFromStaleContext is docs/
// BRIEF-final-demo-fixes.md #1's exact transcript, reproduced end to
// end: "someone took goods" -> "Emma" -> "how much for Emma?" -> "5k"
// (the already-correct earlier part of the transcript) establishes
// Emma as the last-referenced customer; a *later*, unrelated "someone
// took goods" must still ask who it's for — never silently reuse
// Emma — and a reply that still names no one ("3k") must not complete
// the record either.
func TestProcessor_SlotFill_IdentityNeverAssumedFromStaleContext(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348200000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},                                  // "Someone took goods" (1st)
		{Intent: ai.IntentCreateDebt, AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // "5k"
		{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},                                  // "Someone took goods" (2nd, later)
		{Intent: ai.IntentCreateDebt, AmountMinor: new(int64(300000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // "3k", still naming no one
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	// --- Already-correct earlier part of the transcript ---
	askWho1, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fdf1.1", "text", new("Someone took goods")))
	if err != nil {
		t.Fatalf("1 (someone took goods): %v", err)
	}
	if !strings.Contains(strings.ToLower(askWho1.Text), "who") {
		t.Fatalf("got reply %q, want it to ask who this is for first", askWho1.Text)
	}

	askAmountForEmma, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fdf1.2", "text", new("Emma")))
	if err != nil {
		t.Fatalf("2 (Emma): %v", err)
	}
	if !strings.Contains(strings.ToLower(askAmountForEmma.Text), "how much") || !strings.Contains(askAmountForEmma.Text, "Emma") {
		t.Fatalf("got reply %q, want it to ask how much for Emma", askAmountForEmma.Text)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fdf1.3", "text", new("5k"))); err != nil {
		t.Fatalf("3 (5k): %v", err)
	}
	var emmaCustomerID int64
	var emmaDebtCountBefore int
	if err := pool.QueryRow(context.Background(),
		`SELECT c.id, (SELECT count(*) FROM debts WHERE customer_id = c.id) FROM customers c WHERE user_id = $1 AND name = 'Emma'`, userID,
	).Scan(&emmaCustomerID, &emmaDebtCountBefore); err != nil {
		t.Fatalf("query Emma: %v", err)
	}
	if emmaDebtCountBefore != 1 {
		t.Fatalf("got %d debts for Emma after the first exchange, want 1", emmaDebtCountBefore)
	}

	// --- The bug: a later, unrelated "someone took goods" ---
	askWho2, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fdf1.4", "text", new("Someone took goods")))
	if err != nil {
		t.Fatalf("4 (someone took goods, again): %v", err)
	}
	if strings.Contains(strings.ToLower(askWho2.Text), "how much") {
		t.Fatalf("got reply %q, want it to ask WHO first, not silently assume Emma and ask for the amount", askWho2.Text)
	}
	if !strings.Contains(strings.ToLower(askWho2.Text), "who") {
		t.Fatalf("got reply %q, want it to ask who this is for", askWho2.Text)
	}

	stillAskingWho, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fdf1.5", "text", new("3k")))
	if err != nil {
		t.Fatalf("5 (3k, naming no one): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE customer_id = $1`, emmaCustomerID); got != emmaDebtCountBefore {
		t.Fatalf("got %d debts for Emma after \"3k\" named no one, want still %d — must never silently attach to stale context", got, emmaDebtCountBefore)
	}
	if strings.Contains(stillAskingWho.Text, "Emma") {
		t.Fatalf("got reply %q, want it to never mention Emma — this exchange has nothing to do with her", stillAskingWho.Text)
	}

	// --- Recovery: once a real name is given, it completes correctly ---
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fdf1.6", "text", new("Ngozi"))); err != nil {
		t.Fatalf("6 (Ngozi): %v", err)
	}
	var ngoziDebtAmount int64
	if err := pool.QueryRow(context.Background(),
		`SELECT d.amount_minor FROM debts d JOIN customers c ON c.id = d.customer_id WHERE c.user_id = $1 AND c.name = 'Ngozi'`, userID,
	).Scan(&ngoziDebtAmount); err != nil {
		t.Fatalf("query Ngozi's debt: %v", err)
	}
	if ngoziDebtAmount != 300000 {
		t.Fatalf("got Ngozi's debt amount %d, want 300000 (3k)", ngoziDebtAmount)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE customer_id = $1`, emmaCustomerID); got != emmaDebtCountBefore {
		t.Fatalf("got %d debts for Emma at the end, want still %d — the 3k debt belongs to Ngozi", got, emmaDebtCountBefore)
	}
}

// TestProcessor_ListOutstandingDebts_GroupsMultipleDebtsPerCustomer is
// docs/BRIEF-final-demo-fixes.md #4's exact transcript: Emmanuel with
// three outstanding debts and Emma with two must each appear as ONE
// customer entry with their debts listed underneath and a per-customer
// subtotal, not as five disconnected, non-adjacent entries.
func TestProcessor_ListOutstandingDebts_GroupsMultipleDebtsPerCustomer(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348200000002")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	emmanuel, err := customers.Create(context.Background(), userID, "Emmanuel", nil, nil)
	if err != nil {
		t.Fatalf("seed Emmanuel: %v", err)
	}
	emma, err := customers.Create(context.Background(), userID, "Emma", nil, nil)
	if err != nil {
		t.Fatalf("seed Emma: %v", err)
	}
	// Interleaved on purpose (Emmanuel, Emma, Emmanuel, Emmanuel, Emma)
	// — grouping must not depend on debts already being adjacent.
	if _, err := debts.Create(context.Background(), userID, emmanuel.ID, money.New(3000000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, emma.ID, money.New(1000000, money.NGN), "beans", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, emmanuel.ID, money.New(2000000, money.NGN), "cement", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, emmanuel.ID, money.New(500000, money.NGN), "crocs", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, emma.ID, money.New(750000, money.NGN), "shoes", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentListOutstandingDebts, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fdf4.1", "text", new("Who owes me?")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if strings.Count(reply.Text, "*Emmanuel*") != 1 {
		t.Fatalf("got reply %q, want exactly one *Emmanuel* header, not one per debt", reply.Text)
	}
	if strings.Count(reply.Text, "*Emma*") != 1 {
		t.Fatalf("got reply %q, want exactly one *Emma* header, not one per debt", reply.Text)
	}
	// Emmanuel's 3 debts (30,000 + 20,000 + 5,000 = 55,000) must appear
	// contiguously, before Emma's own block starts.
	emmanuelIdx := strings.Index(reply.Text, "*Emmanuel*")
	emmaIdx := strings.Index(reply.Text, "*Emma*")
	emmanuelBlock := reply.Text[emmanuelIdx:emmaIdx]
	if emmaIdx < emmanuelIdx {
		emmanuelBlock = reply.Text[emmanuelIdx:]
	}
	for _, want := range []string{"30,000", "20,000", "5,000"} {
		if !strings.Contains(emmanuelBlock, want) {
			t.Fatalf("got Emmanuel's block %q, want it to contain %q", emmanuelBlock, want)
		}
	}
	if !strings.Contains(emmanuelBlock, "Subtotal") || !strings.Contains(emmanuelBlock, "55,000") {
		t.Fatalf("got Emmanuel's block %q, want a subtotal of 55,000", emmanuelBlock)
	}
	// Emma has 2 debts (10,000 + 7,500 = 17,500) — also grouped with a
	// subtotal.
	if !strings.Contains(reply.Text, "17,500") {
		t.Fatalf("got reply %q, want Emma's subtotal 17,500", reply.Text)
	}
	// Grand total: 55,000 + 17,500 = 72,500.
	if !strings.Contains(reply.Text, "72,500") {
		t.Fatalf("got reply %q, want the grand total 72,500", reply.Text)
	}
}

// TestProcessor_ListOutstandingDebts_SingleDebtCustomer_NoSubtotal
// confirms a customer with only one outstanding debt doesn't get a
// redundant subtotal line repeating the same figure.
func TestProcessor_ListOutstandingDebts_SingleDebtCustomer_NoSubtotal(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348200000003")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentListOutstandingDebts, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fdf4.2", "text", new("Who owes me?")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if strings.Contains(reply.Text, "Subtotal") {
		t.Fatalf("got reply %q, want no subtotal line for a single-debt customer", reply.Text)
	}
}

// TestProcessor_CustomerStatement_MissingDescription_OmitsLabel is
// docs/BRIEF-final-demo-fixes.md #5's exact transcript: a debt with no
// description must not render the literal word "item" — that piece of
// the line should just not be there, while a debt that does have a
// description still renders it correctly.
func TestProcessor_CustomerStatement_MissingDescription_OmitsLabel(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348200000004")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt with description: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c.ID, money.New(500000, money.NGN), "", nil); err != nil {
		t.Fatalf("seed debt with no description: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentGetCustomerStatement, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fdf5.1", "text", new("show Chinedu's account")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if strings.Contains(strings.ToLower(reply.Text), "*item*") {
		t.Fatalf("got statement %q, want no literal \"item\" placeholder", reply.Text)
	}
	if !strings.Contains(reply.Text, "*rice*") {
		t.Fatalf("got statement %q, want the debt with a description to still render it", reply.Text)
	}
	if !strings.Contains(reply.Text, "5,000") {
		t.Fatalf("got statement %q, want the description-less debt's amount to still render", reply.Text)
	}
}
