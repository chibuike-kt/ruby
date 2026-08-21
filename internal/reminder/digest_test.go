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
	"github.com/chibuike-kt/ruby/internal/payment"
	"github.com/chibuike-kt/ruby/internal/reminder"
)

func lastDigestSentAt(t *testing.T, pool *pgxpool.Pool, userID int64) *time.Time {
	t.Helper()
	var ts *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT last_digest_sent_at FROM users WHERE id = $1`, userID).Scan(&ts); err != nil {
		t.Fatalf("query last_digest_sent_at: %v", err)
	}
	return ts
}

// TestDispatchDigests_SendsWeeklySummary is docs/BRIEF-research-
// hardening-standard.md Part 5 Tier 1's own list: transactions recorded,
// amount collected, amount outstanding, anything due soon — sent
// through the same TemplateSender mechanism reminders already use.
func TestDispatchDigests_SendsWeeklySummary(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348220000001")
	c, err := customer.Create(context.Background(), pool, customer.Customer{UserID: userID, Name: "Chinedu"})
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	dueSoon := time.Now().Add(48 * time.Hour)
	d, err := debt.NewService(pool).Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", &dueSoon)
	if err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	if _, err := payment.NewService(pool).Record(context.Background(), userID, d.ID, money.New(2000000, money.NGN), "pay-1"); err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	sender := &fakeTemplateSender{}
	svc := reminder.NewService(pool, sender, "debt_reminder_customer", "debt_reminder_trader", "weekly_digest")

	sent, failed, err := svc.DispatchDigests(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("DispatchDigests: %v", err)
	}
	if sent != 1 || failed != 0 {
		t.Fatalf("got sent=%d failed=%d, want sent=1 failed=0", sent, failed)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("got %d template calls, want 1", len(sender.calls))
	}
	call := sender.calls[0]
	if call.templateName != "weekly_digest" {
		t.Fatalf("got template %q, want weekly_digest", call.templateName)
	}
	if len(call.bodyParams) != 5 {
		t.Fatalf("got %d body params, want 5 (name, transactions, collected, outstanding, due-soon)", len(call.bodyParams))
	}
	if call.bodyParams[0] != "Test Trader" {
		t.Fatalf("got trader name %q, want Test Trader", call.bodyParams[0])
	}
	if call.bodyParams[1] != "1" {
		t.Fatalf("got transactions recorded %q, want 1 (the one debt created)", call.bodyParams[1])
	}
	if call.bodyParams[2] != money.FormatNaira(2000000) {
		t.Fatalf("got amount collected %q, want %s", call.bodyParams[2], money.FormatNaira(2000000))
	}
	if call.bodyParams[3] != money.FormatNaira(5500000) {
		t.Fatalf("got amount outstanding %q, want %s (75k debt minus 20k paid)", call.bodyParams[3], money.FormatNaira(5500000))
	}
	if call.bodyParams[4] != "1" {
		t.Fatalf("got due-soon count %q, want 1", call.bodyParams[4])
	}

	sentAt := lastDigestSentAt(t, pool, userID)
	if sentAt == nil {
		t.Fatal("got last_digest_sent_at still nil after a successful send")
	}
}

// TestDispatchDigests_SkipsUserAlreadyDigestedThisWeek confirms the
// weekly cadence: a trader digested moments ago doesn't get a second
// one on the very next dispatch tick.
func TestDispatchDigests_SkipsUserAlreadyDigestedThisWeek(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348220000002")
	now := time.Now()
	sender := &fakeTemplateSender{}
	svc := reminder.NewService(pool, sender, "debt_reminder_customer", "debt_reminder_trader", "weekly_digest")

	if _, _, err := svc.DispatchDigests(context.Background(), now); err != nil {
		t.Fatalf("DispatchDigests (first): %v", err)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("got %d calls after the first tick, want 1", len(sender.calls))
	}

	if _, _, err := svc.DispatchDigests(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("DispatchDigests (second, a minute later): %v", err)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("got %d calls after the second tick, want still 1 — a week hasn't passed", len(sender.calls))
	}
	_ = userID
}

// TestDispatchDigests_SendsAgainAfterAWeek confirms the cadence resumes
// once a full week has actually elapsed since the last digest.
func TestDispatchDigests_SendsAgainAfterAWeek(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.CreateUser(t, pool, "+2348220000003")
	now := time.Now()
	sender := &fakeTemplateSender{}
	svc := reminder.NewService(pool, sender, "debt_reminder_customer", "debt_reminder_trader", "weekly_digest")

	if _, _, err := svc.DispatchDigests(context.Background(), now); err != nil {
		t.Fatalf("DispatchDigests (week 1): %v", err)
	}
	if _, _, err := svc.DispatchDigests(context.Background(), now.Add(8*24*time.Hour)); err != nil {
		t.Fatalf("DispatchDigests (week 2): %v", err)
	}
	if len(sender.calls) != 2 {
		t.Fatalf("got %d calls across two weeks, want 2", len(sender.calls))
	}
}

// TestDispatchDigests_SkipsUnnamedAccount confirms an account still
// mid-onboarding (no name yet) never receives a digest.
func TestDispatchDigests_SkipsUnnamedAccount(t *testing.T) {
	pool := dbtest.Open(t)
	var userID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (name, phone_number, business_name) VALUES ('', $1, '') RETURNING id`, "+2348220000004",
	).Scan(&userID); err != nil {
		t.Fatalf("seed nameless user: %v", err)
	}

	sender := &fakeTemplateSender{}
	svc := reminder.NewService(pool, sender, "debt_reminder_customer", "debt_reminder_trader", "weekly_digest")

	sent, _, err := svc.DispatchDigests(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("DispatchDigests: %v", err)
	}
	if sent != 0 || len(sender.calls) != 0 {
		t.Fatalf("got sent=%d calls=%d, want 0 — a nameless (mid-onboarding) account must never get a digest", sent, len(sender.calls))
	}
}

// TestDispatchDigests_ZeroActivityWeek_StillSends confirms a quiet week
// is itself reported, not silently suppressed.
func TestDispatchDigests_ZeroActivityWeek_StillSends(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.CreateUser(t, pool, "+2348220000005")

	sender := &fakeTemplateSender{}
	svc := reminder.NewService(pool, sender, "debt_reminder_customer", "debt_reminder_trader", "weekly_digest")

	sent, failed, err := svc.DispatchDigests(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("DispatchDigests: %v", err)
	}
	if sent != 1 || failed != 0 {
		t.Fatalf("got sent=%d failed=%d, want sent=1 failed=0 even with zero activity", sent, failed)
	}
	call := sender.calls[0]
	if call.bodyParams[1] != "0" || call.bodyParams[3] != money.FormatNaira(0) {
		t.Fatalf("got body params %v, want zeroes for transactions/outstanding on a quiet week", call.bodyParams)
	}
}
