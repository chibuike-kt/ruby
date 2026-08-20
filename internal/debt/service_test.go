package debt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/ledger"
	"github.com/chibuike-kt/ruby/internal/money"
)

func setupCustomer(t *testing.T, pool db.Querier, userID int64) customer.Customer {
	t.Helper()
	c, err := customer.Create(context.Background(), pool, customer.Customer{UserID: userID, Name: "Ngozi"})
	if err != nil {
		t.Fatalf("setup customer: %v", err)
	}
	return c
}

func TestCreate_Success(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348020000001")
	cust := setupCustomer(t, pool, userID)
	svc := debt.NewService(pool)

	amount, err := money.NewFromMajor(120000, money.NGN)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	d, err := svc.Create(context.Background(), userID, cust.ID, amount, "5 bags of rice", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Status != debt.StatusOutstanding {
		t.Fatalf("got status %s, want OUTSTANDING", d.Status)
	}
	if d.Amount.MinorUnits() != 12000000 {
		t.Fatalf("got %d minor units, want 12000000", d.Amount.MinorUnits())
	}

	entries, err := ledger.ListByDebt(context.Background(), pool, d.ID)
	if err != nil {
		t.Fatalf("ledger lookup: %v", err)
	}
	if len(entries) != 1 || entries[0].Type != ledger.EntryDebtCreated || entries[0].AmountMinor != 12000000 {
		t.Fatalf("got ledger entries %+v, want single DEBT_CREATED +12000000", entries)
	}
}

// TestCreate_NoDueDate is docs/BRIEF-critical-fixes-and-reminders.md
// #1a's own requirement, made explicit: a nil due date is a normal,
// valid record (spec §21 — "Ruby records the debt but should not
// invent a due date"), not an error condition. TestCreate_Success
// already exercises this implicitly (its own dueDate arg is nil); this
// test exists so the requirement is asserted by name, not just
// incidentally covered.
func TestCreate_NoDueDate(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348020000099")
	cust := setupCustomer(t, pool, userID)
	svc := debt.NewService(pool)

	d, err := svc.Create(context.Background(), userID, cust.ID, money.New(5000000, money.NGN), "no due date given", nil)
	if err != nil {
		t.Fatalf("unexpected error creating a debt with no due date: %v", err)
	}
	if d.DueDate != nil {
		t.Fatalf("got due date %v, want nil — never invent one", d.DueDate)
	}
	if d.Status != debt.StatusOutstanding {
		t.Fatalf("got status %s, want OUTSTANDING", d.Status)
	}
}

func TestCreate_InvalidAmount(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348020000002")
	cust := setupCustomer(t, pool, userID)
	svc := debt.NewService(pool)
	ctx := context.Background()

	zero := money.New(0, money.NGN)
	if _, err := svc.Create(ctx, userID, cust.ID, zero, "", nil); !errors.Is(err, debt.ErrInvalidAmount) {
		t.Fatalf("zero amount: got %v, want ErrInvalidAmount", err)
	}

	negative := money.New(-100, money.NGN)
	if _, err := svc.Create(ctx, userID, cust.ID, negative, "", nil); !errors.Is(err, debt.ErrInvalidAmount) {
		t.Fatalf("negative amount: got %v, want ErrInvalidAmount", err)
	}
}

func TestCreate_CustomerNotOwnedByUser(t *testing.T) {
	pool := dbtest.Open(t)
	ownerID := dbtest.CreateUser(t, pool, "+2348020000003")
	attackerID := dbtest.CreateUser(t, pool, "+2348020000004")
	cust := setupCustomer(t, pool, ownerID)
	svc := debt.NewService(pool)

	amount := money.New(1000000, money.NGN)
	_, err := svc.Create(context.Background(), attackerID, cust.ID, amount, "", nil)
	if !errors.Is(err, customer.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound (spec §35 cross-user protection)", err)
	}
}

func TestGetByID_CrossUserAccessDenied(t *testing.T) {
	pool := dbtest.Open(t)
	ownerID := dbtest.CreateUser(t, pool, "+2348020000005")
	otherID := dbtest.CreateUser(t, pool, "+2348020000006")
	cust := setupCustomer(t, pool, ownerID)
	svc := debt.NewService(pool)
	ctx := context.Background()

	created, err := svc.Create(ctx, ownerID, cust.ID, money.New(1000000, money.NGN), "", nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err = svc.Get(ctx, otherID, created.ID)
	if !errors.Is(err, debt.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
