package debt_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/money"
)

// TestCustomerAndDebtCreation_AtomicRollback is docs/BRIEF-critical-
// fixes-and-reminders.md #1b's required regression test: simulate a
// failure partway through debt creation (here, the debts table's
// amount_minor > 0 CHECK constraint, tripped by a negative amount that
// never reaches this layer through the real AI/HTTP paths — this test
// exists specifically to force a failure *after* the customer insert
// has already run inside the same transaction, which nothing in the
// public API can trigger on its own) and confirm the whole sequence
// rolls back cleanly — this is exactly the pattern
// internal/ai.Processor.executeCreateDebt now uses: customer.CreateChecked
// and debt.CreateWithLedger sharing one db.WithTx.
func TestCustomerAndDebtCreation_AtomicRollback(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348020000099")
	ctx := context.Background()

	const customerName = "Atomicity Test Customer"

	var customerCreateErr error
	err := db.WithTx(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		c, err := customer.CreateChecked(ctx, tx, userID, customerName, nil, nil)
		customerCreateErr = err
		if err != nil {
			return err
		}
		// A negative amount can never actually reach this point through
		// the real validate.go (parseAmount rejects it long before
		// CreateDebtAction is built) — that's the point: this forces a
		// failure at the SQL layer specifically *after* the customer
		// row already exists in this same, uncommitted transaction.
		_, err = debt.CreateWithLedger(ctx, tx, debt.Debt{
			UserID:     userID,
			CustomerID: c.ID,
			Amount:     money.New(-1000000, money.NGN),
			Status:     debt.StatusOutstanding,
		})
		return err
	})
	if customerCreateErr != nil {
		t.Fatalf("customer creation inside the transaction should succeed: %v", customerCreateErr)
	}
	if err == nil {
		t.Fatal("expected the debt write to fail (negative amount), got nil error")
	}

	// (a) No orphaned customer row survives — the whole transaction,
	// including the customer insert, must have rolled back.
	matches, findErr := customer.FindByName(ctx, pool, userID, customerName)
	if findErr != nil {
		t.Fatalf("FindByName: %v", findErr)
	}
	if len(matches) != 0 {
		t.Fatalf("got %d customer rows named %q after a failed debt creation, want 0 — the customer insert must have rolled back too", len(matches), customerName)
	}

	// (b) A subsequent identical request doesn't error out as a
	// duplicate, and doesn't silently find a leftover row — it creates
	// a genuinely fresh customer, proving nothing survived from the
	// failed attempt.
	fresh, err := customer.CreateChecked(ctx, pool, userID, customerName, nil, nil)
	if err != nil {
		t.Fatalf("a subsequent identical request should succeed cleanly, got: %v", err)
	}
	again, err := customer.FindByName(ctx, pool, userID, customerName)
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if len(again) != 1 || again[0].ID != fresh.ID {
		t.Fatalf("got %d customers named %q after the retry, want exactly 1 (the fresh one, id=%d)", len(again), customerName, fresh.ID)
	}
}
