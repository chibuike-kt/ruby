package creditprofile_test

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/ruby/internal/creditprofile"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/money"
	"github.com/chibuike-kt/ruby/internal/payment"
)

func TestGet_NoActivity_ZeroesAndFullOnTimeRate(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348100000001")

	p, err := creditprofile.Get(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.TotalCreditIssuedMinor != 0 || p.TotalCollectedMinor != 0 || p.OutstandingMinor != 0 {
		t.Fatalf("got non-zero totals for a trader with no activity: %+v", p)
	}
	if p.ActiveCustomerCount != 0 {
		t.Fatalf("got active customer count %d, want 0", p.ActiveCustomerCount)
	}
	// No payments to be late on — a fresh trader shouldn't score worse
	// than an established one just for lacking history.
	if p.OnTimePaymentRate != 1.0 {
		t.Fatalf("got on-time rate %v, want 1.0 for a trader with no payments yet", p.OnTimePaymentRate)
	}
}

func TestGet_TraderNamePrefersBusinessName(t *testing.T) {
	pool := dbtest.Open(t)
	var userID int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (name, phone_number, business_name) VALUES ($1, $2, $3) RETURNING id
	`, "Musa", "+2348100000002", "Musa Trading").Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	p, err := creditprofile.Get(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.TraderName != "Musa Trading" {
		t.Fatalf("got trader name %q, want the business name Musa Trading", p.TraderName)
	}
}

func TestGet_ActiveCustomerCount_ExcludesSettledDebts(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348100000003")
	debts := debt.NewService(pool)

	// Customer A: fully paid off (settled) — not "active" anymore.
	custA, err := customer.Create(context.Background(), pool, customer.Customer{UserID: userID, Name: "Ada"})
	if err != nil {
		t.Fatalf("seed customer A: %v", err)
	}
	debtA, err := debts.Create(context.Background(), userID, custA.ID, money.New(5000000, money.NGN), "settled debt", nil)
	if err != nil {
		t.Fatalf("seed debt A: %v", err)
	}
	if _, err := payment.NewService(pool).Record(context.Background(), userID, debtA.ID, money.New(5000000, money.NGN), "idem-a"); err != nil {
		t.Fatalf("seed payment A: %v", err)
	}

	// Customer B: still outstanding — this is the "active" one.
	custB, err := customer.Create(context.Background(), pool, customer.Customer{UserID: userID, Name: "Bola"})
	if err != nil {
		t.Fatalf("seed customer B: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, custB.ID, money.New(3000000, money.NGN), "outstanding debt", nil); err != nil {
		t.Fatalf("seed debt B: %v", err)
	}

	p, err := creditprofile.Get(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.ActiveCustomerCount != 1 {
		t.Fatalf("got active customer count %d, want 1 (only the still-outstanding one)", p.ActiveCustomerCount)
	}
	if p.TotalCreditIssuedMinor != 8000000 {
		t.Fatalf("got total credit issued %d, want 8000000 (both debts)", p.TotalCreditIssuedMinor)
	}
	if p.OutstandingMinor != 3000000 {
		t.Fatalf("got outstanding %d, want 3000000 (only customer B's debt)", p.OutstandingMinor)
	}
}

func TestGet_OnTimePaymentRate_MixedOnTimeAndLate(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348100000004")
	cust, err := customer.Create(context.Background(), pool, customer.Customer{UserID: userID, Name: "Chinedu"})
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	debts := debt.NewService(pool)
	payments := payment.NewService(pool)

	futureDue := time.Now().Add(24 * time.Hour)
	onTimeDebt, err := debts.Create(context.Background(), userID, cust.ID, money.New(1000000, money.NGN), "paid on time", &futureDue)
	if err != nil {
		t.Fatalf("seed on-time debt: %v", err)
	}
	if _, err := payments.Record(context.Background(), userID, onTimeDebt.ID, money.New(1000000, money.NGN), "idem-ontime"); err != nil {
		t.Fatalf("seed on-time payment: %v", err)
	}

	pastDue := time.Now().Add(-48 * time.Hour)
	lateDebt, err := debts.Create(context.Background(), userID, cust.ID, money.New(1000000, money.NGN), "paid late", &pastDue)
	if err != nil {
		t.Fatalf("seed late debt: %v", err)
	}
	if _, err := payments.Record(context.Background(), userID, lateDebt.ID, money.New(1000000, money.NGN), "idem-late"); err != nil {
		t.Fatalf("seed late payment: %v", err)
	}

	// No due date at all — excluded from the rate entirely, not counted
	// as either on-time or late.
	if _, err := debts.Create(context.Background(), userID, cust.ID, money.New(500000, money.NGN), "no due date", nil); err != nil {
		t.Fatalf("seed no-due-date debt: %v", err)
	}

	p, err := creditprofile.Get(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.OnTimePaymentRate != 0.5 {
		t.Fatalf("got on-time rate %v, want 0.5 (1 of 2 due-dated payments on time)", p.OnTimePaymentRate)
	}
}

func TestGet_AccountAgeDays(t *testing.T) {
	pool := dbtest.Open(t)
	var userID int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (name, phone_number, created_at, updated_at)
		VALUES ('Test', '+2348100000005', now() - interval '10 days', now())
		RETURNING id
	`).Scan(&userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	p, err := creditprofile.Get(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if p.AccountAgeDays != 10 {
		t.Fatalf("got account age %d days, want 10", p.AccountAgeDays)
	}
}
