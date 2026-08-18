package payment_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/ledger"
	"github.com/chibuike-kt/ruby/internal/money"
	"github.com/chibuike-kt/ruby/internal/payment"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fixture creates a user, a customer, and a debt of the given major-unit
// amount, returning the ids tests need.
func fixture(t *testing.T, pool *pgxpool.Pool, phone string, majorAmount int64) (userID, debtID int64) {
	t.Helper()
	ctx := context.Background()

	userID = dbtest.CreateUser(t, pool, phone)
	cust, err := customer.Create(ctx, pool, customer.Customer{UserID: userID, Name: "Chinedu"})
	if err != nil {
		t.Fatalf("setup customer: %v", err)
	}

	amount, err := money.NewFromMajor(majorAmount, money.NGN)
	if err != nil {
		t.Fatalf("setup amount: %v", err)
	}
	debtSvc := debt.NewService(pool)
	d, err := debtSvc.Create(ctx, userID, cust.ID, amount, "", nil)
	if err != nil {
		t.Fatalf("setup debt: %v", err)
	}
	return userID, d.ID
}

func TestRecord_PartialPayment(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000001", 120000)
	svc := payment.NewService(pool)
	ctx := context.Background()

	amount, _ := money.NewFromMajor(50000, money.NGN)
	res, err := svc.Record(ctx, userID, debtID, amount, "key-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Debt.Status != debt.StatusPartiallyPaid {
		t.Fatalf("got status %s, want PARTIALLY_PAID", res.Debt.Status)
	}

	outstandingMinor := res.Debt.Amount.MinorUnits() - 5000000
	if outstandingMinor != 7000000 {
		t.Fatalf("got outstanding %d, want 7000000", outstandingMinor)
	}
}

// Spec §13's own example: 120,000 debt, 50,000 then 20,000 paid, landing
// on 50,000 outstanding and still PARTIALLY_PAID.
func TestRecord_MultiplePartialPayments(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000002", 120000)
	svc := payment.NewService(pool)
	ctx := context.Background()

	first, _ := money.NewFromMajor(50000, money.NGN)
	if _, err := svc.Record(ctx, userID, debtID, first, "key-a"); err != nil {
		t.Fatalf("first payment: %v", err)
	}

	second, _ := money.NewFromMajor(20000, money.NGN)
	res, err := svc.Record(ctx, userID, debtID, second, "key-b")
	if err != nil {
		t.Fatalf("second payment: %v", err)
	}
	if res.Debt.Status != debt.StatusPartiallyPaid {
		t.Fatalf("got status %s, want PARTIALLY_PAID", res.Debt.Status)
	}

	total, err := payment.SumByDebt(ctx, pool, debtID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if total != 7000000 {
		t.Fatalf("got total paid %d, want 7000000 (70,000)", total)
	}
}

func TestRecord_FullPayment_SettlesDebt(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000003", 50000)
	svc := payment.NewService(pool)
	ctx := context.Background()

	amount, _ := money.NewFromMajor(50000, money.NGN)
	res, err := svc.Record(ctx, userID, debtID, amount, "key-full")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Debt.Status != debt.StatusSettled {
		t.Fatalf("got status %s, want SETTLED", res.Debt.Status)
	}

	entries, err := ledger.ListByDebt(ctx, pool, debtID)
	if err != nil {
		t.Fatalf("ledger lookup: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d ledger entries, want 3 (DEBT_CREATED, PAYMENT_RECORDED, DEBT_SETTLED)", len(entries))
	}
	if entries[2].Type != ledger.EntryDebtSettled {
		t.Fatalf("got last entry type %s, want DEBT_SETTLED", entries[2].Type)
	}
}

func TestRecord_Overpayment_Rejected(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000004", 75000)
	svc := payment.NewService(pool)
	ctx := context.Background()

	attempted, _ := money.NewFromMajor(100000, money.NGN)
	_, err := svc.Record(ctx, userID, debtID, attempted, "key-over")

	var overpay *payment.OverpaymentError
	if !errors.As(err, &overpay) {
		t.Fatalf("got %v, want OverpaymentError (spec §14)", err)
	}
	if overpay.Outstanding.MinorUnits() != 7500000 {
		t.Fatalf("got outstanding %d, want 7500000", overpay.Outstanding.MinorUnits())
	}

	paid, err := payment.SumByDebt(ctx, pool, debtID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if paid != 0 {
		t.Fatalf("got %d paid, want 0 — rejected overpayment must not touch the balance", paid)
	}
}

func TestRecord_ZeroOrNegativeAmount_Rejected(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000005", 50000)
	svc := payment.NewService(pool)
	ctx := context.Background()

	if _, err := svc.Record(ctx, userID, debtID, money.New(0, money.NGN), "key-zero"); !errors.Is(err, payment.ErrInvalidAmount) {
		t.Fatalf("zero: got %v, want ErrInvalidAmount", err)
	}
	if _, err := svc.Record(ctx, userID, debtID, money.New(-100, money.NGN), "key-neg"); !errors.Is(err, payment.ErrInvalidAmount) {
		t.Fatalf("negative: got %v, want ErrInvalidAmount", err)
	}
}

func TestRecord_AgainstSettledDebt_Rejected(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000006", 50000)
	svc := payment.NewService(pool)
	ctx := context.Background()

	full, _ := money.NewFromMajor(50000, money.NGN)
	if _, err := svc.Record(ctx, userID, debtID, full, "key-settle"); err != nil {
		t.Fatalf("setup: %v", err)
	}

	more, _ := money.NewFromMajor(10000, money.NGN)
	_, err := svc.Record(ctx, userID, debtID, more, "key-after-settle")
	if !errors.Is(err, payment.ErrDebtSettled) {
		t.Fatalf("got %v, want ErrDebtSettled", err)
	}
}

// Spec §15/§31: the same event arriving twice must not double-deduct.
func TestRecord_DuplicateIdempotencyKey_NoDoubleDeduction(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000007", 120000)
	svc := payment.NewService(pool)
	ctx := context.Background()

	amount, _ := money.NewFromMajor(50000, money.NGN)
	first, err := svc.Record(ctx, userID, debtID, amount, "shared-key")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Replayed {
		t.Fatal("first call should not be marked as replayed")
	}

	second, err := svc.Record(ctx, userID, debtID, amount, "shared-key")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if !second.Replayed {
		t.Fatal("second call with the same idempotency key should be marked as replayed")
	}
	if second.Payment.ID != first.Payment.ID {
		t.Fatalf("replay returned a different payment: got %d, want %d", second.Payment.ID, first.Payment.ID)
	}

	total, err := payment.SumByDebt(ctx, pool, debtID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if total != 5000000 {
		t.Fatalf("got total paid %d, want 5000000 — duplicate must not double-deduct", total)
	}
}

func TestRecord_IdempotencyKeyConflict_DifferentAmount(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000008", 120000)
	svc := payment.NewService(pool)
	ctx := context.Background()

	first, _ := money.NewFromMajor(50000, money.NGN)
	if _, err := svc.Record(ctx, userID, debtID, first, "reused-key"); err != nil {
		t.Fatalf("first: %v", err)
	}

	second, _ := money.NewFromMajor(20000, money.NGN)
	_, err := svc.Record(ctx, userID, debtID, second, "reused-key")

	var conflict *payment.IdempotencyConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v, want IdempotencyConflictError", err)
	}
}

// Spec §32: if any step fails, nothing partially applied remains. A
// currency mismatch is caught inside the locked transaction, after the
// debt row lock is already held — proving the rollback actually
// discards the in-flight work rather than leaving a stray payment.
func TestRecord_FailedTransactionRollsBack(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000009", 50000)
	svc := payment.NewService(pool)
	ctx := context.Background()

	wrongCurrency := money.New(5000000, money.USD)
	_, err := svc.Record(ctx, userID, debtID, wrongCurrency, "key-bad-currency")
	if !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("got %v, want ErrCurrencyMismatch", err)
	}

	paid, err := payment.SumByDebt(ctx, pool, debtID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if paid != 0 {
		t.Fatalf("got %d paid after rollback, want 0", paid)
	}

	entries, err := ledger.ListByDebt(ctx, pool, debtID)
	if err != nil {
		t.Fatalf("ledger lookup: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d ledger entries after rollback, want 1 (only the original DEBT_CREATED)", len(entries))
	}
}

// The mandatory race condition test, spec §34: 10 concurrent ₦50,000
// payment requests against a ₦50,000 debt must yield exactly 1 success,
// 9 rejections, and a final outstanding balance of exactly ₦0 — never
// 10 payments, a negative balance, or duplicate ledger events.
func TestRecord_ConcurrentPayments_ExactlyOneSucceeds(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000010", 50000)
	svc := payment.NewService(pool)

	const attempts = 10
	amount, _ := money.NewFromMajor(50000, money.NGN)

	var wg sync.WaitGroup
	results := make([]error, attempts)
	wg.Add(attempts)
	for i := range attempts {
		go func(i int) {
			defer wg.Done()
			_, err := svc.Record(context.Background(), userID, debtID, amount, fmt.Sprintf("concurrent-key-%d", i))
			results[i] = err
		}(i)
	}
	wg.Wait()

	successes, rejections := 0, 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			rejections++
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful payments, want exactly 1", successes)
	}
	if rejections != attempts-1 {
		t.Fatalf("got %d rejected payments, want %d", rejections, attempts-1)
	}

	total, err := payment.SumByDebt(context.Background(), pool, debtID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if total != 5000000 {
		t.Fatalf("got total paid %d, want exactly 5000000 (one payment, not %d)", total, attempts)
	}

	finalDebt, err := debt.NewService(pool).Get(context.Background(), userID, debtID)
	if err != nil {
		t.Fatalf("get debt: %v", err)
	}
	if finalDebt.Status != debt.StatusSettled {
		t.Fatalf("got status %s, want SETTLED", finalDebt.Status)
	}
	outstanding := finalDebt.Amount.MinorUnits() - total
	if outstanding != 0 {
		t.Fatalf("got outstanding %d, want 0", outstanding)
	}

	payments, err := payment.ListByDebt(context.Background(), pool, debtID)
	if err != nil {
		t.Fatalf("list payments: %v", err)
	}
	if len(payments) != 1 {
		t.Fatalf("got %d payment rows, want exactly 1 — never %d", len(payments), attempts)
	}

	entries, err := ledger.ListByDebt(context.Background(), pool, debtID)
	if err != nil {
		t.Fatalf("ledger lookup: %v", err)
	}
	paymentEntries, settledEntries := 0, 0
	for _, e := range entries {
		switch e.Type {
		case ledger.EntryPaymentRecorded:
			paymentEntries++
		case ledger.EntryDebtSettled:
			settledEntries++
		}
	}
	if paymentEntries != 1 {
		t.Fatalf("got %d PAYMENT_RECORDED ledger entries, want exactly 1 (no duplicate ledger events)", paymentEntries)
	}
	if settledEntries != 1 {
		t.Fatalf("got %d DEBT_SETTLED ledger entries, want exactly 1", settledEntries)
	}
}

// A stricter variant of §34: the same idempotency key (not 10 distinct
// ones) fired concurrently, simulating a provider retry racing its own
// original request. Exactly one of the racers must win the unique
// constraint on payments.idempotency_key; the other must resolve to the
// same payment via replay, never a second deduction.
func TestRecord_ConcurrentDuplicateIdempotencyKey_NoDoubleDeduction(t *testing.T) {
	pool := dbtest.Open(t)
	userID, debtID := fixture(t, pool, "+2348030000011", 120000)
	svc := payment.NewService(pool)
	amount, _ := money.NewFromMajor(50000, money.NGN)

	const attempts = 5
	var wg sync.WaitGroup
	results := make([]payment.Result, attempts)
	errs := make([]error, attempts)
	wg.Add(attempts)
	for i := range attempts {
		go func(i int) {
			defer wg.Done()
			res, err := svc.Record(context.Background(), userID, debtID, amount, "racing-key")
			results[i] = res
			errs[i] = err
		}(i)
	}
	wg.Wait()

	var paymentID int64
	for i, err := range errs {
		if err != nil {
			t.Fatalf("attempt %d: unexpected error %v", i, err)
		}
		if paymentID == 0 {
			paymentID = results[i].Payment.ID
		} else if results[i].Payment.ID != paymentID {
			t.Fatalf("attempt %d resolved to payment %d, want %d", i, results[i].Payment.ID, paymentID)
		}
	}

	total, err := payment.SumByDebt(context.Background(), pool, debtID)
	if err != nil {
		t.Fatalf("sum: %v", err)
	}
	if total != 5000000 {
		t.Fatalf("got total paid %d, want exactly 5000000 — no double deduction from the race", total)
	}
}
