package ai_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

// TestProcessor_ReminderOptIn_TTLOutlivesDefaultPendingWindow is docs/
// BRIEF-research-hardening-standard.md Part 5 live-testing finding #4:
// a "Yes, remind them" tap 21 minutes after debt creation fell through
// to the generic fallback because the reminder opt-in's pending state
// had already expired under the standard 10-minute DefaultPendingTTL —
// a standalone yes/no offer isn't part of an active exchange the
// trader might have abandoned, so it needs a much longer window.
func TestProcessor_ReminderOptIn_TTLOutlivesDefaultPendingWindow(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348250000001")

	dueDate := time.Now().Add(72 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), DueDateISO: &dueDate, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	p := newTestProcessorWithReminders(pool, rdb, extractor, &fakePhraser{}, &fakeSender{}, &fakeTemplateSender{})

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.ttl.1", "text", new("Chinedu took 75k, due Friday"))); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	pending, ok, err := ai.GetPendingAction(context.Background(), rdb, userID)
	if err != nil || !ok || pending.Kind != ai.PendingReminderOptIn {
		t.Fatalf("got pending (ok=%v kind=%v err=%v), want PendingReminderOptIn", ok, pending.Kind, err)
	}

	ttl, err := rdb.TTL(context.Background(), fmt.Sprintf("ruby:ctx:%d:pending", userID)).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= ai.DefaultPendingTTL {
		t.Fatalf("got TTL %v, want longer than DefaultPendingTTL (%v) — a reminder opt-in tap arriving well after 10 minutes must still resolve, not fall through to the generic fallback", ttl, ai.DefaultPendingTTL)
	}

	// A late-but-still-within-window tap must resolve for real, not the
	// generic help fallback — reproduces the exact live-testing report
	// (a tap 21 minutes later) without actually sleeping in the test.
	yesID := "reminder:yes"
	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.ttl.2", "interactive", &yesID))
	if err != nil {
		t.Fatalf("Handle (late yes tap): %v", err)
	}
	if reply.Text == "" {
		t.Fatal("got an empty reply for a late reminder opt-in tap")
	}
}
