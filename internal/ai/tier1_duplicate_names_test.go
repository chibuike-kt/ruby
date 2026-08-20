package ai_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

// TestProcessor_DuplicateName_CaseMismatch_TriggersIdentityThenSignal is
// docs/BRIEF-disambiguation-reminders-statements.md Tier 1a's
// regression test, following the actual transcript: a second
// "Emmanuel" mention (typed with different capitalization — "emmanuel",
// mirroring how a real trader's casual typing or a voice transcript
// might render it) must never silently create an indistinguishable
// duplicate customer. Root cause: FindByName's case-sensitive match
// meant this mention's lookup missed the existing "Emmanuel" row,
// falling straight through to customer creation. With that fixed, the
// mention now correctly resolves to the existing customer and — because
// nothing else corroborates it's the same person — asks the same/new
// question (decisions.md #9) before decisions.md #8's phone-or-alias
// guard gates creating a genuinely new, same-named customer.
func TestProcessor_DuplicateName_CaseMismatch_TriggersIdentityThenSignal(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348180000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Emmanuel"), AmountMinor: new(int64(30000)), Description: new("pure water"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, CustomerName: new("Ngozi"), AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, CustomerName: new("emmanuel"), AmountMinor: new(int64(5000000)), Description: new("crocs"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	// First Emmanuel.
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t1.1", "text", new("Emmanuel took pure water for 300"))); err != nil {
		t.Fatalf("create first Emmanuel: %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM customers WHERE user_id = $1 AND lower(name) = 'emmanuel'`, userID); got != 1 {
		t.Fatalf("got %d Emmanuel customers after the first message, want 1", got)
	}

	// An unrelated customer, so the "last customer" context hint no
	// longer points at Emmanuel — otherwise recent-context corroboration
	// (decisions.md #9 signal 4) would legitimately treat the next
	// mention as the same Emmanuel with no question needed, which is a
	// separate, already-correct behavior this test isn't exercising.
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t1.2", "text", new("Ngozi took 5k"))); err != nil {
		t.Fatalf("create Ngozi: %v", err)
	}

	// Second "Emmanuel," different capitalization, no phone/alias.
	prompt, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t1.3", "text", new("emmanuel took crocs for 50k")))
	if err != nil {
		t.Fatalf("second emmanuel mention: %v", err)
	}
	if !strings.Contains(prompt.Text, "Emmanuel") || len(prompt.Buttons) != 2 {
		t.Fatalf("got reply %+v, want the same/new identity-confirmation prompt (2 buttons) — the case-insensitive fix must resolve \"emmanuel\" to the existing \"Emmanuel\"", prompt)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM customers WHERE user_id = $1 AND lower(name) = 'emmanuel'`, userID); got != 1 {
		t.Fatalf("got %d Emmanuel customers before the identity question is answered, want still 1 — no silent duplicate", got)
	}

	// "Someone new" — decisions.md #8's guard must now require a
	// phone/alias before creating the genuinely-new second Emmanuel.
	signalPrompt, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t1.4", "interactive", new("identity:new")))
	if err != nil {
		t.Fatalf("tap someone-new: %v", err)
	}
	if !strings.Contains(strings.ToLower(signalPrompt.Text), "phone") && !strings.Contains(strings.ToLower(signalPrompt.Text), "alias") {
		t.Fatalf("got reply %q, want a prompt asking for a phone number or alias (decisions.md #8)", signalPrompt.Text)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM customers WHERE user_id = $1 AND lower(name) = 'emmanuel'`, userID); got != 1 {
		t.Fatalf("got %d Emmanuel customers before the phone/alias signal is given, want still 1", got)
	}

	// Supply the alias — now the second, genuinely distinct Emmanuel is
	// created, and the crocs debt lands on it, not the first.
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t1.5", "text", new("crocs guy"))); err != nil {
		t.Fatalf("supply alias: %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM customers WHERE user_id = $1 AND lower(name) = 'emmanuel'`, userID); got != 2 {
		t.Fatalf("got %d Emmanuel customers after the alias was supplied, want 2", got)
	}

	var firstDebts, secondDebts int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM debts d JOIN customers c ON c.id = d.customer_id WHERE c.user_id = $1 AND lower(c.name) = 'emmanuel' AND c.alias IS NULL`, userID,
	).Scan(&firstDebts); err != nil {
		t.Fatalf("count first Emmanuel's debts: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM debts d JOIN customers c ON c.id = d.customer_id WHERE c.user_id = $1 AND lower(c.name) = 'emmanuel' AND c.alias = 'crocs guy'`, userID,
	).Scan(&secondDebts); err != nil {
		t.Fatalf("count second Emmanuel's debts: %v", err)
	}
	if firstDebts != 1 || secondDebts != 1 {
		t.Fatalf("got firstDebts=%d secondDebts=%d, want 1 and 1 — each Emmanuel keeps their own debt", firstDebts, secondDebts)
	}
}

// TestProcessor_TwoDuplicateCustomers_LaterBareReference_Disambiguates
// is docs/BRIEF-disambiguation-reminders-statements.md Tier 1b's
// regression test: with two same-named customers genuinely on file
// (seeded directly, the "true worst case" already established in
// internal/customer's own tests), a later bare-name reference with no
// corroborating context must ask which one, never silently resolve to
// either.
func TestProcessor_TwoDuplicateCustomers_LaterBareReference_Disambiguates(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348180000002")

	var first, second int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO customers (user_id, name) VALUES ($1, 'Emmanuel') RETURNING id`, userID,
	).Scan(&first); err != nil {
		t.Fatalf("seed first Emmanuel: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO customers (user_id, name) VALUES ($1, 'emmanuel') RETURNING id`, userID,
	).Scan(&second); err != nil {
		t.Fatalf("seed second Emmanuel: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO debts (user_id, customer_id, amount_minor, currency, description, status) VALUES ($1, $2, 30000, 'NGN', 'pure water', 'OUTSTANDING')`, userID, first,
	); err != nil {
		t.Fatalf("seed first debt: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentRecordPayment, CustomerName: new("Emmanuel"), AmountMinor: new(int64(1000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t1b.1", "text", new("Emmanuel has paid 10k")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) != 2 {
		t.Fatalf("got %d buttons, want 2 (one per Emmanuel candidate) — a duplicate name must always ask, never silently pick one", len(reply.Buttons))
	}
	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 0 {
		t.Fatalf("got %d payments before the ambiguity is resolved, want 0", got)
	}
}
