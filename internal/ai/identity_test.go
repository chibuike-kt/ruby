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

// TestProcessor_IdentityConfirmation_CreateDebt_TriggersPrompt is
// decisions.md #9's own trigger case (docs/BRIEF-fixes-and-reminders.md
// #3): CREATE_DEBT names a customer whose bare name matches exactly one
// existing customer, with no other signal — Ruby must ask same-or-new
// rather than silently assuming either.
func TestProcessor_IdentityConfirmation_CreateDebt_TriggersPrompt(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000001")
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

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.1", "text", new("Chinedu took 50k")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "Chinedu") {
		t.Fatalf("got reply %q, want the existing customer's name in the prompt", reply.Text)
	}
	if len(reply.Buttons) != 2 {
		t.Fatalf("got %d buttons, want 2 (same/new)", len(reply.Buttons))
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts`); got != 0 {
		t.Fatalf("got %d debts before the same/new question is answered, want 0", got)
	}
}

// TestProcessor_IdentityConfirmation_Same_ResolvesToExistingCustomer
// covers the "Same person" branch: it resolves to the existing customer
// and proceeds normally, without creating a duplicate.
func TestProcessor_IdentityConfirmation_Same_ResolvesToExistingCustomer(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000002")
	customers := customer.NewService(pool)
	existing, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.2a", "text", new("Chinedu took 50k"))); err != nil {
		t.Fatalf("Handle (first message): %v", err)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.2b", "interactive", new("identity:same"))); err != nil {
		t.Fatalf("Handle (same button): %v", err)
	}

	if got := countRows(t, pool, `SELECT count(*) FROM customers WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d customers, want 1 (still just the existing one, not a duplicate)", got)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE customer_id = $1`, existing.ID); got != 1 {
		t.Fatalf("got %d debts against the existing customer, want 1", got)
	}
}

// TestProcessor_IdentityConfirmation_New_AsksForSignalThenCreates covers
// the "Someone new" branch: decisions.md #8's creation guard — a phone
// number or alias is required before a same-named customer is created.
func TestProcessor_IdentityConfirmation_New_AsksForSignalThenCreates(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000003")
	customers := customer.NewService(pool)
	existing, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.3a", "text", new("Chinedu took 50k"))); err != nil {
		t.Fatalf("Handle (first message): %v", err)
	}

	signalPrompt, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.3b", "interactive", new("identity:new")))
	if err != nil {
		t.Fatalf("Handle (new button): %v", err)
	}
	if !strings.Contains(strings.ToLower(signalPrompt.Text), "phone") && !strings.Contains(strings.ToLower(signalPrompt.Text), "alias") {
		t.Fatalf("got reply %q, want a prompt asking for a phone number or alias", signalPrompt.Text)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.3c", "text", new("+2348030000099"))); err != nil {
		t.Fatalf("Handle (phone reply): %v", err)
	}

	if got := countRows(t, pool, `SELECT count(*) FROM customers WHERE user_id = $1 AND name = 'Chinedu'`, userID); got != 2 {
		t.Fatalf("got %d Chinedu customers, want 2 (the existing one plus the new one)", got)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE customer_id = $1`, existing.ID); got != 0 {
		t.Fatalf("got %d debts against the *existing* customer, want 0 — this debt belongs to the new one", got)
	}
	var newCustomerID int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM customers WHERE user_id = $1 AND name = 'Chinedu' AND id != $2`, userID, existing.ID,
	).Scan(&newCustomerID); err != nil {
		t.Fatalf("query new customer: %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE customer_id = $1`, newCustomerID); got != 1 {
		t.Fatalf("got %d debts against the new customer, want 1", got)
	}
}

// TestProcessor_IdentityConfirmation_RecordPayment_TriggersPrompt
// confirms the gate also applies to RECORD_PAYMENT
// (docs/BRIEF-fixes-and-reminders.md #3's "CREATE_DEBT (or
// RECORD_PAYMENT)").
func TestProcessor_IdentityConfirmation_RecordPayment_TriggersPrompt(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000004")
	customers := customer.NewService(pool)
	if _, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentRecordPayment, CustomerName: new("Chinedu"), AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.4", "text", new("Chinedu paid 5k")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) != 2 {
		t.Fatalf("got %d buttons, want 2 (same/new)", len(reply.Buttons))
	}
	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 0 {
		t.Fatalf("got %d payments before the same/new question is answered, want 0", got)
	}
}

// TestProcessor_IdentityConfirmation_CreateDebt_CorroboratedByHint_SkipsPrompt
// and TestProcessor_IdentityConfirmation_RecordPayment_CorroboratedByHint_SkipsPrompt
// are docs/BRIEF-research-hardening-standard.md Part 2's audit: CREATE_DEBT
// and RECORD_PAYMENT both call Validator.resolve with checkIdentity=true
// and are gated by the *identical* logic (see resolve's own doc
// comment) — a recently-referenced customer (spec §8 signal 4)
// corroborates a bare-name match and skips the same/new question for
// both intents equally. What looked like an inconsistency in a real
// transcript (several debt creations against Emmanuel never asked,
// while a payment did) is fully explained by conversational recency —
// each debt creation refreshes the hint to Emmanuel, corroborating the
// next one, while the payment happened to be the first mention of that
// customer in the current context window — not by any CREATE_DEBT-
// vs-RECORD_PAYMENT special-casing. These two tests hold both intents
// to the same standard directly, back to back, so this can never be
// mistaken for a bug again.
func TestProcessor_IdentityConfirmation_CreateDebt_CorroboratedByHint_SkipsPrompt(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000006")
	customers := customer.NewService(pool)
	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := customer.SetLastCustomerContext(context.Background(), rdb, userID, c.ID, customer.DefaultLastCustomerContextTTL); err != nil {
		t.Fatalf("seed last customer context: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.6", "text", new("Chinedu took 5k")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) == 2 {
		t.Fatalf("got the same/new prompt (2 buttons), want it skipped — a corroborating hint resolves CREATE_DEBT identity exactly like it does for RECORD_PAYMENT")
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE customer_id = $1`, c.ID); got != 1 {
		t.Fatalf("got %d debts, want 1 — the corroborated match must resolve directly", got)
	}
}

func TestProcessor_IdentityConfirmation_RecordPayment_CorroboratedByHint_SkipsPrompt(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000007")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)
	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	if err := customer.SetLastCustomerContext(context.Background(), rdb, userID, c.ID, customer.DefaultLastCustomerContextTTL); err != nil {
		t.Fatalf("seed last customer context: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentRecordPayment, CustomerName: new("Chinedu"), AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.7", "text", new("Chinedu paid 5k")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) == 2 {
		t.Fatalf("got the same/new prompt (2 buttons), want it skipped — a corroborating hint resolves RECORD_PAYMENT identity exactly like it does for CREATE_DEBT")
	}
	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 1 {
		t.Fatalf("got %d payments, want 1 — the corroborated match must resolve directly", got)
	}
}

// TestProcessor_IdentityConfirmation_GetCustomerBalance_DoesNotTrigger
// is the required scoping test: a read-only balance lookup never
// attributes anything to anyone, so the same/new gate doesn't apply to
// it (docs/BRIEF-fixes-and-reminders.md #3 names CREATE_DEBT/
// RECORD_PAYMENT only).
func TestProcessor_IdentityConfirmation_GetCustomerBalance_DoesNotTrigger(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000005")
	customers := customer.NewService(pool)
	if _, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentGetCustomerBalance, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.5", "text", new("how much does Chinedu owe")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) != 0 {
		t.Fatalf("got %d buttons, want 0 — a balance lookup must not trigger the same/new prompt", len(reply.Buttons))
	}
	if phraser.last().Event != ai.EventCustomerBalance {
		t.Fatalf("got phrase event %v, want EventCustomerBalance", phraser.last().Event)
	}
}

// TestProcessor_IdentityConfirmation_FreeText_Same is docs/BRIEF-
// polish-and-hardening.md #3's required free-text fallback: typing
// "yes" must resolve identity confirmation exactly like tapping "Same
// person" does — buttons are additive, never a replacement.
func TestProcessor_IdentityConfirmation_FreeText_Same(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000006")
	customers := customer.NewService(pool)
	existing, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.6a", "text", new("Chinedu took 50k"))); err != nil {
		t.Fatalf("Handle (first message): %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.6b", "text", new("yes"))); err != nil {
		t.Fatalf("Handle (free-text yes): %v", err)
	}

	if got := countRows(t, pool, `SELECT count(*) FROM customers WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d customers, want 1 (still just the existing one, not a duplicate)", got)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE customer_id = $1`, existing.ID); got != 1 {
		t.Fatalf("got %d debts against the existing customer, want 1", got)
	}
}

// TestProcessor_IdentityConfirmation_FreeText_Unclear re-asks (with the
// same buttons) rather than silently dropping a reply that isn't
// classifiable as yes or no.
func TestProcessor_IdentityConfirmation_FreeText_Unclear(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348060000007")
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

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.7a", "text", new("Chinedu took 50k"))); err != nil {
		t.Fatalf("Handle (first message): %v", err)
	}
	reask, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.identity.7b", "text", new("blah blah")))
	if err != nil {
		t.Fatalf("Handle (unclear reply): %v", err)
	}
	if !strings.Contains(reask.Text, "Chinedu") || len(reask.Buttons) != 2 {
		t.Fatalf("got reply %+v, want the same/new prompt re-asked with 2 buttons", reask)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts`); got != 0 {
		t.Fatalf("got %d debts, want 0 — an unclear reply must not be treated as an answer", got)
	}
}
