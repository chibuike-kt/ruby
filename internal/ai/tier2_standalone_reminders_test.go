package ai_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/money"
	"github.com/chibuike-kt/ruby/internal/reminder"
)

// TestProcessor_CreateReminder_Standalone_AnytimeAfterDebtCreation is
// docs/BRIEF-disambiguation-reminders-statements.md Tier 2a's own
// transcript: "Can you remind me about his payment tomorrow?" asked
// well after debt creation, on a debt that was created with no due
// date at all (Tier 2b) — this must schedule a real reminder off the
// date given in the request itself, never the old "reminders aren't
// available yet" decline.
func TestProcessor_CreateReminder_Standalone_AnytimeAfterDebtCreation(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348190100001")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// No due date on the debt — Tier 2b's requirement.
	if _, err := debts.Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	if err := customer.SetLastCustomerContext(context.Background(), rdb, userID, c.ID, customer.DefaultLastCustomerContextTTL); err != nil {
		t.Fatalf("seed last customer context: %v", err)
	}

	tomorrowISO := time.Now().Add(24 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateReminder, CustomerName: new("Chinedu"), DueDateISO: &tomorrowISO, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t2a.1", "text", new("Can you remind me about his payment tomorrow?")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if strings.Contains(reply.Text, "aren't available") || strings.Contains(reply.Text, "noted your request") {
		t.Fatalf("got reply %q, want a real scheduling confirmation, not the old decline", reply.Text)
	}
	if !strings.Contains(reply.Text, "Chinedu") {
		t.Fatalf("got reply %q, want it to name Chinedu", reply.Text)
	}

	var scheduled int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM reminders r JOIN debts d ON d.id = r.debt_id WHERE d.customer_id = $1 AND r.recipient_type = 'TRADER'`, c.ID,
	).Scan(&scheduled); err != nil {
		t.Fatalf("count reminders: %v", err)
	}
	if scheduled != 2 {
		t.Fatalf("got %d trader reminders scheduled, want 2 (day-before and on-date, off the date given in the request)", scheduled)
	}
}

// TestProcessor_CreateReminder_MissingDate_AsksThenSchedules is Tier
// 2b's other required behavior: "if the reminder request itself
// doesn't include a date either, ask for one at that point rather than
// declining outright."
func TestProcessor_CreateReminder_MissingDate_AsksThenSchedules(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348190100002")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}

	fridayISO := time.Now().Add(3 * 24 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateReminder, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // no date
		{Intent: ai.IntentCreateReminder, DueDateISO: &fridayISO, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	askDate, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t2b.1", "text", new("remind me about Chinedu")))
	if err != nil {
		t.Fatalf("Handle (missing date): %v", err)
	}
	if !strings.Contains(strings.ToLower(askDate.Text), "when") {
		t.Fatalf("got reply %q, want the slot-fill date question", askDate.Text)
	}
	if len(askDate.Buttons) != 1 || askDate.Buttons[0].ID != "cancel" {
		t.Fatalf("got buttons %+v, want the single Cancel button", askDate.Buttons)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t2b.2", "text", new("Friday"))); err != nil {
		t.Fatalf("Handle (fill date): %v", err)
	}

	var scheduled int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM reminders r JOIN debts d ON d.id = r.debt_id WHERE d.customer_id = $1 AND r.recipient_type = 'TRADER'`, c.ID,
	).Scan(&scheduled); err != nil {
		t.Fatalf("count reminders: %v", err)
	}
	if scheduled != 2 {
		t.Fatalf("got %d trader reminders, want 2", scheduled)
	}
}

// TestProcessor_CreateReminder_NoOutstandingDebt_Declines confirms a
// customer with nothing outstanding gets an honest "nothing to remind
// about" answer, not a crash or a reminder scheduled against nothing.
func TestProcessor_CreateReminder_NoOutstandingDebt_Declines(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348190100003")
	customers := customer.NewService(pool)
	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	tomorrowISO := time.Now().Add(24 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateReminder, CustomerName: new("Chinedu"), DueDateISO: &tomorrowISO, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t2c.1", "text", new("remind me about Chinedu tomorrow"))); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if phraser.last().Event != ai.EventNoOutstandingDebt {
		t.Fatalf("got phrase event %v, want EventNoOutstandingDebt", phraser.last().Event)
	}
	var scheduled int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM reminders`).Scan(&scheduled); err != nil {
		t.Fatalf("count reminders: %v", err)
	}
	if scheduled != 0 {
		t.Fatalf("got %d reminders scheduled, want 0 — Chinedu has nothing outstanding", scheduled)
	}
	_ = c
}

// TestProcessor_CancelReminder_CancelsScheduled is docs/BRIEF-
// disambiguation-reminders-statements.md Tier 2c: a trader can cancel
// a scheduled reminder by referencing the customer it's attached to.
func TestProcessor_CancelReminder_CancelsScheduled(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348190100004")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	dueDate := time.Now().Add(72 * time.Hour)
	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	d, err := debts.Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", &dueDate)
	if err != nil {
		t.Fatalf("seed debt: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCancelReminder, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentCancelReminder, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	// Seed scheduled reminders directly against the debt (mirroring
	// what executeCreateDebt does automatically on a real debt-creation
	// message — debt.Service.Create alone doesn't schedule anything).
	reminders := reminder.NewService(pool, templateSender, "debt_reminder_customer", "debt_reminder_trader")
	if _, err := reminders.ScheduleTraderReminders(context.Background(), d.ID, userID, dueDate); err != nil {
		t.Fatalf("seed reminders: %v", err)
	}

	var beforeCancel int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM reminders WHERE status = 'SCHEDULED'`).Scan(&beforeCancel); err != nil {
		t.Fatalf("count before: %v", err)
	}
	if beforeCancel == 0 {
		t.Fatal("got 0 scheduled reminders before cancelling, want the seeded trader reminders present")
	}

	cancelled, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t2d.1", "text", new("cancel the reminder for Chinedu")))
	if err != nil {
		t.Fatalf("Handle (cancel): %v", err)
	}
	if !strings.Contains(cancelled.Text, "Chinedu") {
		t.Fatalf("got reply %q, want it to confirm cancellation naming Chinedu", cancelled.Text)
	}

	var afterCancel int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM reminders WHERE status = 'SCHEDULED'`).Scan(&afterCancel); err != nil {
		t.Fatalf("count after: %v", err)
	}
	if afterCancel != 0 {
		t.Fatalf("got %d still-scheduled reminders after cancelling, want 0", afterCancel)
	}

	// A second cancel attempt has nothing left to cancel.
	again, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.t2d.2", "text", new("cancel the reminder for Chinedu")))
	if err != nil {
		t.Fatalf("Handle (cancel again): %v", err)
	}
	if !strings.Contains(strings.ToLower(again.Text), "no reminder") {
		t.Fatalf("got reply %q, want an honest \"nothing scheduled\" answer", again.Text)
	}
}
