package ledger_test

import (
	"context"
	"testing"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/ledger"
	"github.com/chibuike-kt/ruby/internal/money"
	"github.com/chibuike-kt/ruby/internal/payment"
)

func TestInsert_And_ListByDebt(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	userID := dbtest.CreateUser(t, pool, "+2348040000001")
	cust, err := customer.Create(ctx, pool, customer.Customer{UserID: userID, Name: "Ada"})
	if err != nil {
		t.Fatalf("setup customer: %v", err)
	}

	debtSvc := debt.NewService(pool)
	amount, _ := money.NewFromMajor(45000, money.NGN)
	d, err := debtSvc.Create(ctx, userID, cust.ID, amount, "", nil)
	if err != nil {
		t.Fatalf("setup debt: %v", err)
	}

	paymentSvc := payment.NewService(pool)
	partial, _ := money.NewFromMajor(20000, money.NGN)
	if _, err := paymentSvc.Record(ctx, userID, d.ID, partial, "key-1"); err != nil {
		t.Fatalf("setup payment: %v", err)
	}

	entries, err := ledger.ListByDebt(ctx, pool, d.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2 (DEBT_CREATED, PAYMENT_RECORDED)", len(entries))
	}
	if entries[0].Type != ledger.EntryDebtCreated || entries[0].AmountMinor != 4500000 {
		t.Fatalf("got first entry %+v, want DEBT_CREATED +4500000", entries[0])
	}
	if entries[1].Type != ledger.EntryPaymentRecorded || entries[1].AmountMinor != -2000000 {
		t.Fatalf("got second entry %+v, want PAYMENT_RECORDED -2000000", entries[1])
	}
}

func TestListByUser_ScopedToOwner(t *testing.T) {
	pool := dbtest.Open(t)
	ctx := context.Background()
	ownerID := dbtest.CreateUser(t, pool, "+2348040000002")
	otherID := dbtest.CreateUser(t, pool, "+2348040000003")

	cust, err := customer.Create(ctx, pool, customer.Customer{UserID: ownerID, Name: "Musa"})
	if err != nil {
		t.Fatalf("setup customer: %v", err)
	}
	amount, _ := money.NewFromMajor(10000, money.NGN)
	if _, err := debt.NewService(pool).Create(ctx, ownerID, cust.ID, amount, "", nil); err != nil {
		t.Fatalf("setup debt: %v", err)
	}

	ownerEntries, err := ledger.ListByUser(ctx, pool, ownerID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ownerEntries) != 1 {
		t.Fatalf("got %d entries for owner, want 1", len(ownerEntries))
	}

	otherEntries, err := ledger.ListByUser(ctx, pool, otherID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(otherEntries) != 0 {
		t.Fatalf("got %d entries for unrelated user, want 0", len(otherEntries))
	}
}
