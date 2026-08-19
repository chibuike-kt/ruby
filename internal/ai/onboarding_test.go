package ai_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/account"
	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

func TestProcessor_Onboarding_FirstContact_AsksForName(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	acct, err := account.Create(context.Background(), pool, "+2348090000001")
	if err != nil {
		t.Fatalf("account.Create: %v", err)
	}

	extractor := &fakeExtractor{} // must never be called: name capture is fully deterministic
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.1", "text", new("Hi Ruby")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "Ruby") {
		t.Fatalf("got reply %q, want the intro to mention Ruby", reply.Text)
	}
	if !strings.Contains(reply.Text, "call you") {
		t.Fatalf("got reply %q, want the name question combined into the same message", reply.Text)
	}
	if len(reply.Buttons) != 3 {
		t.Fatalf("got %d buttons, want the 3 quick-action buttons still attached", len(reply.Buttons))
	}
	if len(extractor.calls) != 0 {
		t.Fatalf("got %d extractor calls, want 0 — name capture never reaches the financial-intent extractor", len(extractor.calls))
	}

	got, err := account.GetByID(context.Background(), pool, acct.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("got name %q after only the first message, want still empty", got.Name)
	}
}

func TestProcessor_Onboarding_NameReply_SavesAcknowledgesAndClearsPending(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	acct, err := account.Create(context.Background(), pool, "+2348090000002")
	if err != nil {
		t.Fatalf("account.Create: %v", err)
	}

	extractor := &fakeExtractor{}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.2a", "text", new("Hi Ruby"))); err != nil {
		t.Fatalf("Handle (first contact): %v", err)
	}

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.2b", "text", new("Chinedu")))
	if err != nil {
		t.Fatalf("Handle (name reply): %v", err)
	}
	if !strings.Contains(reply.Text, "Chinedu") {
		t.Fatalf("got reply %q, want a warm acknowledgment by name", reply.Text)
	}
	if len(extractor.calls) != 0 {
		t.Fatalf("got %d extractor calls, want 0 — the original message was a bare greeting, nothing to reprocess", len(extractor.calls))
	}

	got, err := account.GetByID(context.Background(), pool, acct.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Chinedu" {
		t.Fatalf("got name %q, want Chinedu", got.Name)
	}

	if _, ok, err := ai.GetPendingAction(context.Background(), rdb, acct.ID); err != nil || ok {
		t.Fatalf("got pending action still set after name capture (ok=%v err=%v), want cleared", ok, err)
	}
}

// TestProcessor_Onboarding_RequestLookingReply_ReasksOnceThenFalls is the
// required test: a reply that looks like a real request (contains a
// currency amount) must trigger exactly one re-ask, not immediate
// acceptance as the name — and a second such reply falls back to a
// placeholder rather than looping forever.
func TestProcessor_Onboarding_RequestLookingReply_ReasksOnceThenFalls(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	acct, err := account.Create(context.Background(), pool, "+2348090000003")
	if err != nil {
		t.Fatalf("account.Create: %v", err)
	}

	extractor := &fakeExtractor{}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.3a", "text", new("Hi Ruby"))); err != nil {
		t.Fatalf("Handle (first contact): %v", err)
	}

	reask, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.3b", "text", new("Chinedu took 5k")))
	if err != nil {
		t.Fatalf("Handle (request-looking reply): %v", err)
	}
	if !strings.Contains(strings.ToLower(reask.Text), "name") {
		t.Fatalf("got reply %q, want the more direct re-ask", reask.Text)
	}
	got, err := account.GetByID(context.Background(), pool, acct.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("got name %q after a request-looking reply, want still empty — must not accept it as the name", got.Name)
	}

	// Second non-name-looking reply: stop trying, fall back to a placeholder.
	final, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.3c", "text", new("still 5k for Chinedu")))
	if err != nil {
		t.Fatalf("Handle (second non-name reply): %v", err)
	}
	if !strings.Contains(final.Text, "Trader") {
		t.Fatalf("got reply %q, want the placeholder name acknowledged", final.Text)
	}
	got, err = account.GetByID(context.Background(), pool, acct.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Trader" {
		t.Fatalf("got name %q, want the Trader placeholder — must not loop forever", got.Name)
	}
	if len(extractor.calls) != 0 {
		t.Fatalf("got %d extractor calls, want 0 — the original message was a bare greeting", len(extractor.calls))
	}
}

// TestProcessor_Onboarding_ButtonTapWhileAwaitingName is the required
// test: tapping a quick-action button instead of replying with a name
// is treated as a non-name reply (triggers the re-ask), and the
// button's intent replaces the pending original message so it's
// processed once the name flow resolves.
func TestProcessor_Onboarding_ButtonTapWhileAwaitingName(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	acct, err := account.Create(context.Background(), pool, "+2348090000004")
	if err != nil {
		t.Fatalf("account.Create: %v", err)
	}

	extractor := &fakeExtractor{}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.4a", "text", new("Hi Ruby"))); err != nil {
		t.Fatalf("Handle (first contact): %v", err)
	}

	menuID := "menu:create_debt"
	reask, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.4b", "interactive", &menuID))
	if err != nil {
		t.Fatalf("Handle (button tap while awaiting name): %v", err)
	}
	if !strings.Contains(strings.ToLower(reask.Text), "name") {
		t.Fatalf("got reply %q, want the re-ask — a button tap is not a name", reask.Text)
	}
	got, err := account.GetByID(context.Background(), pool, acct.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "" {
		t.Fatalf("got name %q after a button tap, want still empty", got.Name)
	}

	// Second non-name reply triggers the placeholder fallback, which
	// must then process the *button's* intent (menu:create_debt), not
	// the original "Hi Ruby" greeting it replaced.
	final, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.4c", "text", new("no just record 5k for Chinedu")))
	if err != nil {
		t.Fatalf("Handle (second non-name reply): %v", err)
	}
	if !strings.Contains(final.Text, "Trader") {
		t.Fatalf("got reply %q, want the placeholder acknowledgment", final.Text)
	}
	if !strings.Contains(final.Text, "who and how much") {
		t.Fatalf("got reply %q, want the create-debt prompt (the button's intent) processed after name capture", final.Text)
	}
}

// TestProcessor_Onboarding_OriginalRealContent_ProcessedAfterNameCaptured
// is the required test: a first message with real content beyond a
// greeting is processed automatically right after the name is
// captured, not lost.
func TestProcessor_Onboarding_OriginalRealContent_ProcessedAfterNameCaptured(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	acct, err := account.Create(context.Background(), pool, "+2348090000005")
	if err != nil {
		t.Fatalf("account.Create: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.5a", "text", new("Chinedu took 75k, pays Friday"))); err != nil {
		t.Fatalf("Handle (first contact with real content): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, acct.ID); got != 0 {
		t.Fatalf("got %d debts before the name is even known, want 0", got)
	}

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(acct.ID, "wamid.onboard.5b", "text", new("Ngozi")))
	if err != nil {
		t.Fatalf("Handle (name reply): %v", err)
	}
	if !strings.Contains(reply.Text, "Ngozi") {
		t.Fatalf("got reply %q, want the acknowledgment by name", reply.Text)
	}
	if len(extractor.calls) != 1 {
		t.Fatalf("got %d extractor calls, want exactly 1 — the original message must be reprocessed once, not lost or repeated", len(extractor.calls))
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, acct.ID); got != 1 {
		t.Fatalf("got %d debts after the name was captured, want 1 — the original request must be processed automatically", got)
	}
}
