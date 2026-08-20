package reminder_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/money"
	"github.com/chibuike-kt/ruby/internal/reminder"
)

// setupDebt seeds a trader, a customer, and a debt with a due date,
// returning every id/phone a reminder test typically needs.
func setupDebt(t *testing.T, pool *pgxpool.Pool, traderPhone, customerPhone string, dueDate time.Time) (userID, debtID, customerID int64) {
	t.Helper()
	userID = dbtest.CreateUser(t, pool, traderPhone)
	c, err := customer.Create(context.Background(), pool, customer.Customer{UserID: userID, Name: "Chinedu", PhoneNumber: &customerPhone})
	if err != nil {
		t.Fatalf("setup customer: %v", err)
	}
	d, err := debt.NewService(pool).Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", &dueDate)
	if err != nil {
		t.Fatalf("setup debt: %v", err)
	}
	return userID, d.ID, c.ID
}

func TestScheduleCustomer_CreatesDayBeforeAndDueDateReminders(t *testing.T) {
	pool := dbtest.Open(t)
	dueDate := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	_, debtID, customerID := setupDebt(t, pool, "+2348070000001", "+2348030000001", dueDate)

	rows, err := reminder.ScheduleCustomer(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder_customer")
	if err != nil {
		t.Fatalf("ScheduleCustomer: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d reminders, want 2", len(rows))
	}

	wantTimes := map[time.Time]bool{
		dueDate.AddDate(0, 0, -1): true,
		dueDate:                   true,
	}
	for _, r := range rows {
		if r.DebtID != debtID {
			t.Fatalf("got debt id %d, want %d", r.DebtID, debtID)
		}
		if r.RecipientType != reminder.RecipientCustomer {
			t.Fatalf("got recipient type %v, want CUSTOMER", r.RecipientType)
		}
		if r.RecipientID == nil || *r.RecipientID != customerID {
			t.Fatalf("got recipient id %v, want %d", r.RecipientID, customerID)
		}
		if r.Status != reminder.StatusScheduled {
			t.Fatalf("got status %v, want SCHEDULED", r.Status)
		}
		if !wantTimes[r.ScheduledAt.UTC()] {
			t.Fatalf("got scheduled_at %v, not one of the expected two times", r.ScheduledAt)
		}
		delete(wantTimes, r.ScheduledAt.UTC())
	}
	if len(wantTimes) != 0 {
		t.Fatalf("missing expected scheduled times: %v", wantTimes)
	}
}

// TestScheduleTrader_CreatesDayBeforeAndDueDateReminders is the full
// reminder system's own requirement (docs/BRIEF-critical-fixes-and-
// reminders.md): automatic trader reminders, same day-before/due-date
// pair, no opt-in — recipient is the trader (userID), not the customer.
func TestScheduleTrader_CreatesDayBeforeAndDueDateReminders(t *testing.T) {
	pool := dbtest.Open(t)
	dueDate := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	userID, debtID, _ := setupDebt(t, pool, "+2348070000011", "+2348030000011", dueDate)

	rows, err := reminder.ScheduleTrader(context.Background(), pool, debtID, userID, dueDate, "debt_reminder_trader")
	if err != nil {
		t.Fatalf("ScheduleTrader: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d reminders, want 2", len(rows))
	}
	for _, r := range rows {
		if r.RecipientType != reminder.RecipientTrader {
			t.Fatalf("got recipient type %v, want TRADER", r.RecipientType)
		}
		if r.RecipientID == nil || *r.RecipientID != userID {
			t.Fatalf("got recipient id %v, want %d (the trader)", r.RecipientID, userID)
		}
	}
}

func TestDueForDispatch_OnlyReturnsScheduledAtOrBeforeNow(t *testing.T) {
	pool := dbtest.Open(t)
	traderPhone, customerPhone := "+2348070000002", "+2348030000002"

	pastDue := time.Now().Add(-48 * time.Hour)
	userID, debtID, customerID := setupDebt(t, pool, traderPhone, customerPhone, pastDue)
	if _, err := reminder.ScheduleCustomer(context.Background(), pool, debtID, customerID, pastDue, "debt_reminder_customer"); err != nil {
		t.Fatalf("ScheduleCustomer (past): %v", err)
	}

	futureDue := time.Now().Add(30 * 24 * time.Hour)
	_, futureDebtID, futureCustomerID := setupDebt(t, pool, "+2348070000012", "+2348030000012", futureDue)
	if _, err := reminder.ScheduleCustomer(context.Background(), pool, futureDebtID, futureCustomerID, futureDue, "debt_reminder_customer"); err != nil {
		t.Fatalf("ScheduleCustomer (future): %v", err)
	}

	due, err := reminder.DueForDispatch(context.Background(), pool, time.Now(), 100)
	if err != nil {
		t.Fatalf("DueForDispatch: %v", err)
	}
	for _, d := range due {
		if d.Reminder.DebtID == futureDebtID {
			t.Fatalf("got a future reminder in the due batch: %+v", d)
		}
	}
	// Both of the past debt's reminders (day-before and due-date) are
	// already at-or-before now.
	found := 0
	for _, d := range due {
		if d.Reminder.DebtID == debtID {
			found++
			if d.CustomerName != "Chinedu" {
				t.Fatalf("got customer name %q, want Chinedu", d.CustomerName)
			}
			if d.RecipientPhone != customerPhone {
				t.Fatalf("got recipient phone %q, want the customer's %q", d.RecipientPhone, customerPhone)
			}
			if d.AmountMinor != 7500000 {
				t.Fatalf("got amount minor %d, want 7500000", d.AmountMinor)
			}
		}
	}
	if found != 2 {
		t.Fatalf("got %d due reminders for the past debt, want 2", found)
	}
	_ = userID
}

func TestMarkSent_And_MarkFailed(t *testing.T) {
	pool := dbtest.Open(t)
	dueDate := time.Now().Add(-time.Hour)
	_, debtID, customerID := setupDebt(t, pool, "+2348070000003", "+2348030000003", dueDate)

	rows, err := reminder.ScheduleCustomer(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder_customer")
	if err != nil {
		t.Fatalf("ScheduleCustomer: %v", err)
	}

	if err := reminder.MarkSent(context.Background(), pool, rows[0].ID, "wamid.sent.1", time.Now()); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}
	if err := reminder.MarkFailed(context.Background(), pool, rows[1].ID, "no approved template"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	due, err := reminder.DueForDispatch(context.Background(), pool, time.Now(), 100)
	if err != nil {
		t.Fatalf("DueForDispatch: %v", err)
	}
	for _, d := range due {
		if d.Reminder.ID == rows[0].ID || d.Reminder.ID == rows[1].ID {
			t.Fatalf("got a SENT/FAILED reminder still returned as due: %+v", d)
		}
	}
}

// TestMarkProcessing_ClaimsOnce is spec §20's SCHEDULED -> PROCESSING
// transition (docs/BRIEF-critical-fixes-and-reminders.md's full
// reminder system): the first claim succeeds, a second claim on the
// same row (simulating a concurrent dispatcher) does not.
func TestMarkProcessing_ClaimsOnce(t *testing.T) {
	pool := dbtest.Open(t)
	dueDate := time.Now().Add(-time.Hour)
	_, debtID, customerID := setupDebt(t, pool, "+2348070000006", "+2348030000006", dueDate)
	rows, err := reminder.ScheduleCustomer(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder_customer")
	if err != nil {
		t.Fatalf("ScheduleCustomer: %v", err)
	}

	claimed, err := reminder.MarkProcessing(context.Background(), pool, rows[0].ID)
	if err != nil {
		t.Fatalf("MarkProcessing (first): %v", err)
	}
	if !claimed {
		t.Fatal("got claimed=false on the first attempt, want true")
	}

	claimedAgain, err := reminder.MarkProcessing(context.Background(), pool, rows[0].ID)
	if err != nil {
		t.Fatalf("MarkProcessing (second): %v", err)
	}
	if claimedAgain {
		t.Fatal("got claimed=true on a second attempt against an already-PROCESSING row, want false")
	}
}

// fakeTemplateSender records every call so Dispatch tests can assert on
// exactly what would be sent — the templated-message shape, never
// freeform text.
type fakeTemplateSender struct {
	calls []sentTemplate
	err   error
}

type sentTemplate struct {
	to, templateName, languageCode string
	bodyParams                     []string
}

func (f *fakeTemplateSender) SendTemplate(_ context.Context, to, templateName, languageCode string, bodyParams []string) (string, error) {
	f.calls = append(f.calls, sentTemplate{to, templateName, languageCode, bodyParams})
	if f.err != nil {
		return "", f.err
	}
	return "wamid.template.1", nil
}

func TestDispatch_SendsCustomerReminderViaTemplate(t *testing.T) {
	pool := dbtest.Open(t)
	customerPhone := "+2348030000004"
	dueDate := time.Now().Add(-time.Hour)
	_, debtID, customerID := setupDebt(t, pool, "+2348070000004", customerPhone, dueDate)
	if _, err := reminder.ScheduleCustomer(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder_customer"); err != nil {
		t.Fatalf("ScheduleCustomer: %v", err)
	}

	sender := &fakeTemplateSender{}
	svc := reminder.NewService(pool, sender, "debt_reminder_customer", "debt_reminder_trader")

	sent, failed, err := svc.Dispatch(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sent != 2 || failed != 0 {
		t.Fatalf("got sent=%d failed=%d, want sent=2 failed=0", sent, failed)
	}
	if len(sender.calls) != 2 {
		t.Fatalf("got %d SendTemplate calls, want 2", len(sender.calls))
	}
	for _, c := range sender.calls {
		if c.to != customerPhone {
			t.Fatalf("got recipient %q, want the customer's phone %q — never the trader's", c.to, customerPhone)
		}
		if c.templateName != "debt_reminder_customer" {
			t.Fatalf("got template %q, want debt_reminder_customer", c.templateName)
		}
		if len(c.bodyParams) != 4 {
			t.Fatalf("got %d body params, want 4 (customer name, amount, trader name, due date)", len(c.bodyParams))
		}
	}

	due, err := reminder.DueForDispatch(context.Background(), pool, time.Now(), 100)
	if err != nil {
		t.Fatalf("DueForDispatch after send: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("got %d still-due reminders after a successful dispatch, want 0", len(due))
	}
}

// TestDispatch_SendsTraderReminderViaTemplate confirms the automatic
// trader path uses the trader's own template and phone number, and a
// relative-day word instead of a formatted date (spec §18's example:
// "Chinedu's ₦75,000 payment is due today").
func TestDispatch_SendsTraderReminderViaTemplate(t *testing.T) {
	pool := dbtest.Open(t)
	traderPhone := "+2348070000005"
	dueDate := time.Now().Add(-time.Hour)
	userID, debtID, _ := setupDebt(t, pool, traderPhone, "+2348030000005", dueDate)
	if _, err := reminder.ScheduleTrader(context.Background(), pool, debtID, userID, dueDate, "debt_reminder_trader"); err != nil {
		t.Fatalf("ScheduleTrader: %v", err)
	}

	sender := &fakeTemplateSender{}
	svc := reminder.NewService(pool, sender, "debt_reminder_customer", "debt_reminder_trader")

	sent, failed, err := svc.Dispatch(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sent != 2 || failed != 0 {
		t.Fatalf("got sent=%d failed=%d, want sent=2 failed=0", sent, failed)
	}
	for _, c := range sender.calls {
		if c.to != traderPhone {
			t.Fatalf("got recipient %q, want the trader's phone %q", c.to, traderPhone)
		}
		if c.templateName != "debt_reminder_trader" {
			t.Fatalf("got template %q, want debt_reminder_trader", c.templateName)
		}
		if len(c.bodyParams) != 3 {
			t.Fatalf("got %d body params, want 3 (customer name, amount, relative day)", len(c.bodyParams))
		}
		if c.bodyParams[0] != "Chinedu" {
			t.Fatalf("got body param[0] %q, want the customer's name Chinedu", c.bodyParams[0])
		}
	}
}

// TestDispatch_SettledDebt_Cancelled is a defensive correctness check:
// a reminder for a debt that's already fully paid off must not be
// sent — it's marked CANCELLED instead (spec §20 lists CANCELLED as a
// real state).
func TestDispatch_SettledDebt_Cancelled(t *testing.T) {
	pool := dbtest.Open(t)
	dueDate := time.Now().Add(-time.Hour)
	userID, debtID, customerID := setupDebt(t, pool, "+2348070000007", "+2348030000007", dueDate)
	if _, err := reminder.ScheduleCustomer(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder_customer"); err != nil {
		t.Fatalf("ScheduleCustomer: %v", err)
	}

	// Fully pay off the debt before the reminder ever dispatches.
	if _, err := pool.Exec(context.Background(), `UPDATE debts SET status = 'SETTLED' WHERE id = $1`, debtID); err != nil {
		t.Fatalf("settle debt: %v", err)
	}
	_ = userID

	sender := &fakeTemplateSender{}
	svc := reminder.NewService(pool, sender, "debt_reminder_customer", "debt_reminder_trader")

	sent, failed, err := svc.Dispatch(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sent != 0 || failed != 0 {
		t.Fatalf("got sent=%d failed=%d, want sent=0 failed=0 — a settled debt's reminder is cancelled, not sent or failed", sent, failed)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("got %d SendTemplate calls, want 0 — must never message about an already-settled debt", len(sender.calls))
	}

	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM reminders WHERE debt_id = $1 LIMIT 1`, debtID).Scan(&status); err != nil {
		t.Fatalf("query reminder status: %v", err)
	}
	if status != "CANCELLED" {
		t.Fatalf("got reminder status %q, want CANCELLED", status)
	}
}

func TestDispatch_SendFailure_MarksFailedNotSilently(t *testing.T) {
	pool := dbtest.Open(t)
	dueDate := time.Now().Add(-time.Hour)
	_, debtID, customerID := setupDebt(t, pool, "+2348070000008", "+2348030000008", dueDate)
	if _, err := reminder.ScheduleCustomer(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder_customer"); err != nil {
		t.Fatalf("ScheduleCustomer: %v", err)
	}

	sender := &fakeTemplateSender{err: errUnapprovedTemplate}
	svc := reminder.NewService(pool, sender, "debt_reminder_customer", "debt_reminder_trader")

	sent, failed, err := svc.Dispatch(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sent != 0 || failed != 2 {
		t.Fatalf("got sent=%d failed=%d, want sent=0 failed=2 — a send failure must be recorded, not dropped", sent, failed)
	}

	var reason *string
	if err := pool.QueryRow(context.Background(), `SELECT failure_reason FROM reminders WHERE debt_id = $1 LIMIT 1`, debtID).Scan(&reason); err != nil {
		t.Fatalf("query failure reason: %v", err)
	}
	if reason == nil || *reason == "" {
		t.Fatal("got no failure reason recorded, want the send error stored")
	}
}

var errUnapprovedTemplate = &templateNotApprovedError{}

type templateNotApprovedError struct{}

func (*templateNotApprovedError) Error() string {
	return "whatsapp: template not approved (132001)"
}
