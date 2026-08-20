package ai_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/payment"
	"github.com/chibuike-kt/ruby/internal/reminder"
)

// fakeTemplateSender satisfies reminder.TemplateSender and records every
// call, so tests can assert the actual send attempt uses the templated-
// message shape (docs/BRIEF-fixes-and-reminders.md #4's testing
// requirement), never freeform text.
type fakeTemplateSender struct {
	calls []fakeTemplateCall
}

type fakeTemplateCall struct {
	to, templateName, languageCode string
	bodyParams                     []string
}

func (f *fakeTemplateSender) SendTemplate(_ context.Context, to, templateName, languageCode string, bodyParams []string) (string, error) {
	f.calls = append(f.calls, fakeTemplateCall{to, templateName, languageCode, bodyParams})
	return "wamid.template.1", nil
}

func newTestProcessorWithReminders(pool *pgxpool.Pool, rdb *redis.Client, extractor ai.Extractor, phraser ai.Phraser, sender ai.Sender, templateSender reminder.TemplateSender) *ai.Processor {
	return ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{},
		Transcoder:  fakeTranscoder{},
		Phraser:     phraser,
		Sender:      sender,
		Media:       fakeMedia{},
		Pool:        pool,
		Redis:       rdb,
		Customers:   customer.NewService(pool),
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
		Reminders:   reminder.NewService(pool, templateSender, "debt_reminder_customer", "debt_reminder_trader"),
	})
}

// TestProcessor_AutoTraderReminders_ScheduledWithoutOptIn is the full
// reminder system's own core requirement (docs/BRIEF-critical-fixes-
// and-reminders.md): a debt with a due date gets two automatic trader
// reminders — no opt-in question, no interaction required, unlike the
// customer flow. Verified with the customer opt-in declined, to prove
// the two are genuinely independent.
func TestProcessor_AutoTraderReminders_ScheduledWithoutOptIn(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348080000099")

	dueDate := time.Now().Add(72 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), DueDateISO: &dueDate, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.autoremind.1", "text", new("Chinedu took 50k, due Friday"))); err != nil {
		t.Fatalf("Handle (create debt): %v", err)
	}

	// No interaction at all beyond debt creation — the trader reminders
	// must already exist.
	var debtID int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM debts WHERE user_id = $1`, userID).Scan(&debtID); err != nil {
		t.Fatalf("query debt id: %v", err)
	}

	rows, err := pool.Query(context.Background(), `SELECT recipient_id FROM reminders WHERE debt_id = $1 AND recipient_type = 'TRADER'`, debtID)
	if err != nil {
		t.Fatalf("query trader reminders: %v", err)
	}
	defer rows.Close()

	var recipientIDs []int64
	for rows.Next() {
		var recipientID *int64
		if err := rows.Scan(&recipientID); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if recipientID == nil {
			t.Fatal("got a trader reminder with no recipient_id")
		}
		recipientIDs = append(recipientIDs, *recipientID)
	}

	if len(recipientIDs) != 2 {
		t.Fatalf("got %d trader reminders, want 2 (day-before and due-date), no opt-in required", len(recipientIDs))
	}
	for _, id := range recipientIDs {
		if id != userID {
			t.Fatalf("got trader reminder recipient %d, want the trader's own user id %d", id, userID)
		}
	}
}

// TestProcessor_ReminderOptIn_OfferedOnlyWithDueDate is the required
// scoping test: no due date means nothing to count down to, so the
// question isn't offered at all.
func TestProcessor_ReminderOptIn_OfferedOnlyWithDueDate(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348080000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.1", "text", new("Chinedu took 50k")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) != 0 {
		t.Fatalf("got %d buttons, want 0 — no due date means nothing to remind about", len(reply.Buttons))
	}
}

// TestProcessor_ReminderOptIn_No_NoFurtherAction is the required "No"
// branch test.
func TestProcessor_ReminderOptIn_No_NoFurtherAction(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348080000002")

	dueDate := time.Now().Add(72 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), DueDateISO: &dueDate, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	created, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.2a", "text", new("Chinedu took 50k, due Friday")))
	if err != nil {
		t.Fatalf("Handle (create debt): %v", err)
	}
	if !strings.Contains(created.Text, "Chinedu") || len(created.Buttons) != 2 {
		t.Fatalf("got reply %+v, want the reminder opt-in question with 2 buttons", created)
	}

	declined, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.2b", "interactive", new("reminder:no")))
	if err != nil {
		t.Fatalf("Handle (no): %v", err)
	}
	if !strings.Contains(declined.Text, "No problem") {
		t.Fatalf("got reply %q, want a neutral acknowledgment", declined.Text)
	}
	// Declining the *customer* opt-in must not touch the automatic
	// *trader* reminders — those were already scheduled unconditionally
	// at debt-creation time and are a fully independent decision
	// (docs/BRIEF-critical-fixes-and-reminders.md's full reminder
	// system: "no separate consent needed").
	if got := countRows(t, pool, `SELECT count(*) FROM reminders WHERE recipient_type = 'CUSTOMER'`); got != 0 {
		t.Fatalf("got %d customer reminders scheduled after declining, want 0", got)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM reminders WHERE recipient_type = 'TRADER'`); got != 2 {
		t.Fatalf("got %d trader reminders, want 2 — automatic scheduling is unaffected by the customer opt-in answer", got)
	}
}

// TestProcessor_ReminderOptIn_Yes_PhoneAlreadyOnFile_Schedules is the
// required "phone already on file" branch: no extra phone question,
// straight to scheduling.
func TestProcessor_ReminderOptIn_Yes_PhoneAlreadyOnFile_Schedules(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348080000003")
	customers := customer.NewService(pool)
	seeded, err := customers.Create(context.Background(), userID, "Chinedu", new("+2348030000010"), nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	// Recent conversational context (spec §8 signal 4) so the bare-name
	// CREATE_DEBT match resolves directly instead of triggering
	// decisions.md #9's identity confirmation (docs/BRIEF-fixes-and-
	// reminders.md #3) — not what this test is exercising.
	if err := customer.SetLastCustomerContext(context.Background(), rdb, userID, seeded.ID, customer.DefaultLastCustomerContextTTL); err != nil {
		t.Fatalf("seed last customer context: %v", err)
	}

	dueDate := time.Now().Add(72 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), DueDateISO: &dueDate, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.3a", "text", new("Chinedu took 50k, due Friday"))); err != nil {
		t.Fatalf("Handle (create debt): %v", err)
	}

	scheduled, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.3b", "interactive", new("reminder:yes")))
	if err != nil {
		t.Fatalf("Handle (yes): %v", err)
	}
	if !strings.Contains(scheduled.Text, "Chinedu") {
		t.Fatalf("got reply %q, want a scheduling confirmation naming Chinedu", scheduled.Text)
	}
	if strings.Contains(strings.ToLower(scheduled.Text), "phone number") {
		t.Fatalf("got reply %q, must not ask for a phone number that's already on file", scheduled.Text)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM reminders WHERE recipient_type = 'CUSTOMER'`); got != 2 {
		t.Fatalf("got %d customer reminders scheduled, want 2 (day-before and due-date)", got)
	}
	// The automatic trader reminders from debt creation are independent
	// and already present alongside these.
	if got := countRows(t, pool, `SELECT count(*) FROM reminders WHERE recipient_type = 'TRADER'`); got != 2 {
		t.Fatalf("got %d trader reminders, want 2 (scheduled automatically at debt creation)", got)
	}
}

// TestProcessor_ReminderOptIn_Yes_NoPhone_AsksThenSchedules is the
// required "no phone on file" branch: ask for it, store it on the
// customer record, then schedule.
func TestProcessor_ReminderOptIn_Yes_NoPhone_AsksThenSchedules(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348080000004")

	dueDate := time.Now().Add(72 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), DueDateISO: &dueDate, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.4a", "text", new("Chinedu took 50k, due Friday"))); err != nil {
		t.Fatalf("Handle (create debt): %v", err)
	}

	phonePrompt, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.4b", "interactive", new("reminder:yes")))
	if err != nil {
		t.Fatalf("Handle (yes): %v", err)
	}
	if !strings.Contains(strings.ToLower(phonePrompt.Text), "phone") {
		t.Fatalf("got reply %q, want a phone number request — none on file yet", phonePrompt.Text)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM reminders WHERE recipient_type = 'CUSTOMER'`); got != 0 {
		t.Fatalf("got %d customer reminders scheduled before the phone is captured, want 0", got)
	}

	scheduled, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.4c", "text", new("+2348030000011")))
	if err != nil {
		t.Fatalf("Handle (phone reply): %v", err)
	}
	if !strings.Contains(scheduled.Text, "Chinedu") {
		t.Fatalf("got reply %q, want a scheduling confirmation naming Chinedu", scheduled.Text)
	}

	var storedPhone *string
	if err := pool.QueryRow(context.Background(),
		`SELECT phone_number FROM customers WHERE user_id = $1 AND name = 'Chinedu'`, userID,
	).Scan(&storedPhone); err != nil {
		t.Fatalf("query customer phone: %v", err)
	}
	if storedPhone == nil || *storedPhone != "+2348030000011" {
		t.Fatalf("got stored phone %v, want +2348030000011", storedPhone)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM reminders WHERE recipient_type = 'CUSTOMER'`); got != 2 {
		t.Fatalf("got %d customer reminders scheduled, want 2", got)
	}
}

// TestProcessor_ReminderDispatch_UsesTemplatedShape is the required
// send-attempt test: the actual dispatch to the customer's number uses
// the templated-message shape, never freeform text — verified by
// asserting on the arguments fakeTemplateSender.SendTemplate actually
// received.
func TestProcessor_ReminderDispatch_UsesTemplatedShape(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348080000005")
	phone := "+2348030000012"

	pastDue := time.Now().Add(-time.Hour)
	dueDateISO := pastDue.Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), DueDateISO: &dueDateISO, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.5a", "text", new("Chinedu took 50k"))); err != nil {
		t.Fatalf("Handle (create debt): %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.5b", "interactive", new("reminder:yes"))); err != nil {
		t.Fatalf("Handle (yes): %v", err)
	}
	// The auto-created customer has no phone yet — this is the same
	// "ask for it" branch TestProcessor_ReminderOptIn_Yes_NoPhone_
	// AsksThenSchedules covers; here it's just how this test gets to a
	// scheduled reminder to dispatch.
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.remind.5c", "text", new(phone))); err != nil {
		t.Fatalf("Handle (phone reply): %v", err)
	}

	svc := reminder.NewService(pool, templateSender, "debt_reminder_customer", "debt_reminder_trader")
	sent, failed, err := svc.Dispatch(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// 2 customer reminders (opt-in) + 2 automatic trader reminders,
	// both due (the debt's due date is already in the past).
	if sent != 4 || failed != 0 {
		t.Fatalf("got sent=%d failed=%d, want sent=4 failed=0", sent, failed)
	}
	if len(templateSender.calls) != 4 {
		t.Fatalf("got %d SendTemplate calls, want 4", len(templateSender.calls))
	}
	var customerCalls, traderCalls int
	for _, call := range templateSender.calls {
		if len(call.bodyParams) == 0 {
			t.Fatalf("got no body params — this must be a templated message, not freeform text")
		}
		switch call.templateName {
		case "debt_reminder_customer":
			customerCalls++
			if call.to != phone {
				t.Fatalf("got recipient %q for the customer template, want the customer's phone %q", call.to, phone)
			}
		case "debt_reminder_trader":
			traderCalls++
			if call.to != "+2348080000005" {
				t.Fatalf("got recipient %q for the trader template, want the trader's own phone", call.to)
			}
		default:
			t.Fatalf("got unexpected template %q", call.templateName)
		}
	}
	if customerCalls != 2 || traderCalls != 2 {
		t.Fatalf("got customerCalls=%d traderCalls=%d, want 2 and 2", customerCalls, traderCalls)
	}
}
