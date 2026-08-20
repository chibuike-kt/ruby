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

func setupDebt(t *testing.T, pool *pgxpool.Pool, userID int64, phone *string, dueDate time.Time) (debtID, customerID int64) {
	t.Helper()
	c, err := customer.Create(context.Background(), pool, customer.Customer{UserID: userID, Name: "Chinedu", PhoneNumber: phone})
	if err != nil {
		t.Fatalf("setup customer: %v", err)
	}
	d, err := debt.NewService(pool).Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", &dueDate)
	if err != nil {
		t.Fatalf("setup debt: %v", err)
	}
	return d.ID, c.ID
}

func TestSchedule_CreatesDayBeforeAndDueDateReminders(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348070000001")
	phone := "+2348030000001"
	dueDate := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	debtID, customerID := setupDebt(t, pool, userID, &phone, dueDate)

	rows, err := reminder.Schedule(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder")
	if err != nil {
		t.Fatalf("Schedule: %v", err)
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

func TestDueForDispatch_OnlyReturnsScheduledAtOrBeforeNow(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348070000002")
	phone := "+2348030000002"

	pastDue := time.Now().Add(-48 * time.Hour)
	debtID, customerID := setupDebt(t, pool, userID, &phone, pastDue)
	if _, err := reminder.Schedule(context.Background(), pool, debtID, customerID, pastDue, "debt_reminder"); err != nil {
		t.Fatalf("Schedule (past): %v", err)
	}

	futureDue := time.Now().Add(30 * 24 * time.Hour)
	futureDebtID, futureCustomerID := setupDebt(t, pool, userID, &phone, futureDue)
	if _, err := reminder.Schedule(context.Background(), pool, futureDebtID, futureCustomerID, futureDue, "debt_reminder"); err != nil {
		t.Fatalf("Schedule (future): %v", err)
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
			if d.CustomerPhone != phone {
				t.Fatalf("got customer phone %q, want %q", d.CustomerPhone, phone)
			}
			if d.AmountMinor != 7500000 {
				t.Fatalf("got amount minor %d, want 7500000", d.AmountMinor)
			}
		}
	}
	if found != 2 {
		t.Fatalf("got %d due reminders for the past debt, want 2", found)
	}
}

func TestMarkSent_And_MarkFailed(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348070000003")
	phone := "+2348030000003"
	dueDate := time.Now().Add(-time.Hour)
	debtID, customerID := setupDebt(t, pool, userID, &phone, dueDate)

	rows, err := reminder.Schedule(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder")
	if err != nil {
		t.Fatalf("Schedule: %v", err)
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

// fakeTemplateSender records every call so Dispatch tests can assert on
// exactly what would be sent — the templated-message shape
// (docs/BRIEF-fixes-and-reminders.md #4), never freeform text.
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

func TestDispatch_SendsDueRemindersViaTemplate(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348070000004")
	phone := "+2348030000004"
	dueDate := time.Now().Add(-time.Hour)
	debtID, customerID := setupDebt(t, pool, userID, &phone, dueDate)
	if _, err := reminder.Schedule(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder"); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	sender := &fakeTemplateSender{}
	svc := reminder.NewService(pool, sender, "debt_reminder")

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
		if c.to != phone {
			t.Fatalf("got recipient %q, want the customer's phone %q — never the trader's", c.to, phone)
		}
		if c.templateName != "debt_reminder" {
			t.Fatalf("got template %q, want debt_reminder", c.templateName)
		}
		if len(c.bodyParams) != 3 {
			t.Fatalf("got %d body params, want 3 (name, amount, date)", len(c.bodyParams))
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

func TestDispatch_SendFailure_MarksFailedNotSilently(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348070000005")
	phone := "+2348030000005"
	dueDate := time.Now().Add(-time.Hour)
	debtID, customerID := setupDebt(t, pool, userID, &phone, dueDate)
	if _, err := reminder.Schedule(context.Background(), pool, debtID, customerID, dueDate, "debt_reminder"); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	sender := &fakeTemplateSender{err: errUnapprovedTemplate}
	svc := reminder.NewService(pool, sender, "debt_reminder")

	sent, failed, err := svc.Dispatch(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if sent != 0 || failed != 2 {
		t.Fatalf("got sent=%d failed=%d, want sent=0 failed=2 — a send failure must be recorded, not dropped", sent, failed)
	}
}

var errUnapprovedTemplate = &templateNotApprovedError{}

type templateNotApprovedError struct{}

func (*templateNotApprovedError) Error() string {
	return "whatsapp: template not approved (132001)"
}
