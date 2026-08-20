package ai_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/money"
)

// TestProcessor_SlotFill_AsksOneFieldAtATime is the interactive slot-
// filling section's core behavior plus edge case #1: a message missing
// both required fields asks for identity first, then amount — never
// both at once, and the customer-slot reply ("Chinedu", a clean name)
// is filled deterministically, with zero extra extractor calls.
func TestProcessor_SlotFill_AsksOneFieldAtATime(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348160000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // no name, no amount
		{Intent: ai.IntentCreateDebt, AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	askCustomer, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot1.1", "text", new("someone took goods, pays Friday")))
	if err != nil {
		t.Fatalf("Handle (missing both): %v", err)
	}
	if !strings.Contains(strings.ToLower(askCustomer.Text), "who") {
		t.Fatalf("got reply %q, want it to ask WHO first (identity before amount)", askCustomer.Text)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts`); got != 0 {
		t.Fatalf("got %d debts before slot-filling completed, want 0", got)
	}

	askAmount, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot1.2", "text", new("Chinedu")))
	if err != nil {
		t.Fatalf("Handle (fill customer): %v", err)
	}
	if !strings.Contains(strings.ToLower(askAmount.Text), "how much") {
		t.Fatalf("got reply %q, want it to now ask for the amount", askAmount.Text)
	}
	if len(extractor.calls) != 1 {
		t.Fatalf("got %d extractor calls after a clean name reply, want 1 (the original message only) — a name-shaped reply is filled deterministically", len(extractor.calls))
	}

	done, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot1.3", "text", new("5k")))
	if err != nil {
		t.Fatalf("Handle (fill amount): %v", err)
	}
	if phraser.last().Event != ai.EventDebtCreated {
		t.Fatalf("got reply %q (event %v), want a completed EventDebtCreated", done.Text, phraser.last().Event)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts, want 1", got)
	}
	if _, ok, err := ai.GetPendingAction(context.Background(), rdb, userID); err != nil || ok {
		t.Fatalf("got a pending action still set after completion (ok=%v err=%v), want cleared", ok, err)
	}
}

// TestProcessor_SlotFill_RecordPayment confirms the same mechanism
// applies to RECORD_PAYMENT, not just CREATE_DEBT.
func TestProcessor_SlotFill_RecordPayment(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348160000099")
	customers := customer.NewService(pool)
	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	debts := debt.NewService(pool)
	if _, err := debts.Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	// Recent conversational context (spec §8 signal 4) so the bare-name
	// match resolves directly instead of triggering decisions.md #9's
	// identity confirmation — not what this test exercises.
	if err := customer.SetLastCustomerContext(context.Background(), rdb, userID, c.ID, customer.DefaultLastCustomerContextTTL); err != nil {
		t.Fatalf("seed last customer context: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentRecordPayment, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // amount missing
		{Intent: ai.IntentRecordPayment, AmountMinor: new(int64(300000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	ask, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slotpay.1", "text", new("Chinedu paid")))
	if err != nil {
		t.Fatalf("Handle (missing amount): %v", err)
	}
	if !strings.Contains(strings.ToLower(ask.Text), "how much") {
		t.Fatalf("got reply %q, want the amount question", ask.Text)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slotpay.2", "text", new("3k"))); err != nil {
		t.Fatalf("Handle (fill amount): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 1 {
		t.Fatalf("got %d payments, want 1", got)
	}
}

// TestProcessor_SlotFill_FillsAndHandlesSecondRequest is edge case #2:
// the reply that completes the missing field also carries a separate,
// complete, read-only request — both must be honored, not just one.
func TestProcessor_SlotFill_FillsAndHandlesSecondRequest(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348160000002")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // amount missing
		{Intent: ai.IntentListOutstandingDebts, AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot2.1", "text", new("Chinedu took goods"))); err != nil {
		t.Fatalf("Handle (missing amount): %v", err)
	}

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot2.2", "text", new("5k, also who owes me overall")))
	if err != nil {
		t.Fatalf("Handle (fill + second request): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts, want 1 — the slot-fill must still complete", got)
	}
	if !strings.Contains(reply.Text, "Chinedu") {
		t.Fatalf("got reply %q, want it to mention the completed debt (Chinedu)", reply.Text)
	}
}

// TestProcessor_SlotFill_ReasksWhenReplyDoesntAnswer is edge case #3:
// a reply that answers neither the pending field nor reads as any real
// request gets a direct re-ask, never a guess.
func TestProcessor_SlotFill_ReasksWhenReplyDoesntAnswer(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348160000003")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentConfirmAction, Language: ai.LangEnglish}, // "yes" — answers nothing
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot3.1", "text", new("Chinedu took goods"))); err != nil {
		t.Fatalf("Handle (missing amount): %v", err)
	}
	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot3.2", "text", new("yes")))
	if err != nil {
		t.Fatalf("Handle (non-answer): %v", err)
	}
	if !strings.Contains(strings.ToLower(reply.Text), "amount") {
		t.Fatalf("got reply %q, want the more direct amount re-ask", reply.Text)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts`); got != 0 {
		t.Fatalf("got %d debts, want 0 — must not guess", got)
	}
	if _, ok, err := ai.GetPendingAction(context.Background(), rdb, userID); err != nil || !ok {
		t.Fatalf("got pending action cleared (ok=%v err=%v), want it still active", ok, err)
	}
}

// TestProcessor_SlotFill_CancelExits is edge case #4, exercised
// directly against slot-fill (also proven general in cancel_test.go).
func TestProcessor_SlotFill_CancelExits(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348160000004")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot4.1", "text", new("someone took goods"))); err != nil {
		t.Fatalf("Handle (missing both): %v", err)
	}
	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot4.2", "text", new("forget it")))
	if err != nil {
		t.Fatalf("Handle (cancel): %v", err)
	}
	if !strings.Contains(strings.ToLower(reply.Text), "cancel") {
		t.Fatalf("got reply %q, want a cancellation acknowledgment", reply.Text)
	}
	if _, ok, err := ai.GetPendingAction(context.Background(), rdb, userID); err != nil || ok {
		t.Fatalf("got a pending action still set (ok=%v err=%v), want cleared", ok, err)
	}
}

// TestProcessor_SlotFill_UnrelatedRequestHandledPendingUntouched is
// edge case #5: a genuinely different, complete request while a
// question is outstanding gets handled on its own terms — the
// original pending slot-fill is left as-is (not force-matched, not
// discarded), so it can still resolve on a later reply.
func TestProcessor_SlotFill_UnrelatedRequestHandledPendingUntouched(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348160000005")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentRecordPayment, AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // customer missing
		{Intent: ai.IntentGetTotalOutstanding, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot5.1", "text", new("someone paid me 5k"))); err != nil {
		t.Fatalf("Handle (missing customer): %v", err)
	}

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot5.2", "text", new("actually, what's my total outstanding")))
	if err != nil {
		t.Fatalf("Handle (unrelated request): %v", err)
	}
	if phraser.last().Event != ai.EventTotalOutstanding {
		t.Fatalf("got reply %q (event %v), want the unrelated request answered (EventTotalOutstanding)", reply.Text, phraser.last().Event)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 0 {
		t.Fatalf("got %d payments, want 0 — the original slot-fill must not have been force-completed", got)
	}
	pending, ok, err := ai.GetPendingAction(context.Background(), rdb, userID)
	if err != nil {
		t.Fatalf("GetPendingAction: %v", err)
	}
	if !ok || pending.Kind != ai.PendingSlotFill {
		t.Fatalf("got pending (ok=%v kind=%v), want the original slot-fill still active", ok, pending.Kind)
	}
}

// TestProcessor_SlotFill_CorrectionUpdatesInPlace is edge case #6: a
// correction to a field already given (even one that isn't the field
// currently being asked about) updates the pending record in place —
// never appended as a separate debt, never requiring a restart.
func TestProcessor_SlotFill_CorrectionUpdatesInPlace(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348160000006")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // missing both
		// "Chinedu" fills customer deterministically (no extractor call).
		// Now amount is pending; this reply corrects the *customer*
		// instead of answering the amount question.
		{Intent: ai.IntentCreateDebt, CustomerName: new("Ngozi"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot6.1", "text", new("someone took goods"))); err != nil {
		t.Fatalf("Handle (missing both): %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot6.2", "text", new("Chinedu"))); err != nil {
		t.Fatalf("Handle (fill customer): %v", err)
	}

	corrected, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot6.3", "text", new("actually it was Ngozi, not Chinedu")))
	if err != nil {
		t.Fatalf("Handle (correction): %v", err)
	}
	if !strings.Contains(corrected.Text, "Ngozi") {
		t.Fatalf("got reply %q, want the re-ask to reflect the corrected name (Ngozi)", corrected.Text)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts`); got != 0 {
		t.Fatalf("got %d debts before the amount was ever given, want 0", got)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot6.4", "text", new("5k"))); err != nil {
		t.Fatalf("Handle (fill amount): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts, want exactly 1 (the correction must update in place, not create a second debt)", got)
	}
	if input := phraser.last(); input.CustomerName != "Ngozi" {
		t.Fatalf("got debt customer %q, want Ngozi — the correction must have taken effect", input.CustomerName)
	}
}

// TestProcessor_SlotFill_MultipleTransactionsDeclined is edge case #7:
// explicitly out of scope — verified this already routes through 1c's
// honest decline (docs/BRIEF-critical-fixes-and-reminders.md's own
// instruction: "decline gracefully per 1c rather than partially
// handling it"), not a new mechanism.
func TestProcessor_SlotFill_MultipleTransactionsDeclined(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348160000007")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentUnsupported, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot7.1", "text", new("Chinedu took 5k and Ngozi took 3k")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !strings.Contains(reply.Text, "can't do that yet") {
		t.Fatalf("got reply %q, want the honest decline, not a half-built extraction", reply.Text)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts`); got != 0 {
		t.Fatalf("got %d debts, want 0", got)
	}
}

// TestProcessor_SlotFill_AllFiveLanguages is edge case #8: the full
// ask-customer-then-amount flow, once per supported language,
// asserting the actual localized question text each time.
func TestProcessor_SlotFill_AllFiveLanguages(t *testing.T) {
	cases := []struct {
		name       string
		lang       ai.Language
		wantWho    string
		wantAmount string
	}{
		{"english", ai.LangEnglish, "Who is this for?", "How much"},
		{"pidgin", ai.LangPidgin, "Na who be dis for?", "How much"},
		{"yoruba", ai.LangYoruba, "Ta ni èyí ṣe fún?", "Èló"},
		{"igbo", ai.LangIgbo, "Onye ka nke a bụ?", "Ego ole"},
		{"hausa", ai.LangHausa, "Wanene wannan?", "Nawa"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := dbtest.Open(t)
			rdb := dbtest.OpenRedis(t)
			userID := dbtest.CreateUser(t, pool, fmt.Sprintf("+234817000%d00", i))

			extractor := &fakeExtractor{results: []ai.RawIntent{
				{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: tc.lang},
				{Intent: ai.IntentCreateDebt, AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: tc.lang},
			}}
			phraser := &fakePhraser{}
			sender := &fakeSender{}
			p := newTestProcessor(pool, rdb, extractor, phraser, sender)

			askCustomer, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot8."+tc.name+".1", "text", new("someone took goods")))
			if err != nil {
				t.Fatalf("Handle (missing both): %v", err)
			}
			if askCustomer.Text != tc.wantWho {
				t.Fatalf("got %q, want the localized customer question %q", askCustomer.Text, tc.wantWho)
			}

			askAmount, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot8."+tc.name+".2", "text", new("Chinedu")))
			if err != nil {
				t.Fatalf("Handle (fill customer): %v", err)
			}
			if !strings.Contains(askAmount.Text, tc.wantAmount) {
				t.Fatalf("got %q, want it to contain the localized amount question fragment %q", askAmount.Text, tc.wantAmount)
			}

			if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.slot8."+tc.name+".3", "text", new("5k"))); err != nil {
				t.Fatalf("Handle (fill amount): %v", err)
			}
			if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
				t.Fatalf("got %d debts, want 1 (language=%s)", got, tc.lang)
			}
		})
	}
}
