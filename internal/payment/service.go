package payment

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/ledger"
	"github.com/chibuike-kt/ruby/internal/money"
)

var (
	ErrInvalidAmount          = errors.New("payment: amount must be positive")
	ErrIdempotencyKeyRequired = errors.New("payment: idempotency key is required")
	ErrDebtSettled            = errors.New("payment: debt is already settled")
	ErrDebtNotFound           = debt.ErrNotFound
)

// OverpaymentError is returned instead of silently accepting a payment
// larger than what remains outstanding (spec §14, decisions.md #6). The
// caller decides how to reprompt the trader (e.g. "did you mean to
// record X as the final payment?") — this package never clamps or
// guesses at the intended amount.
type OverpaymentError struct {
	Outstanding money.Money
	Attempted   money.Money
}

func (e *OverpaymentError) Error() string {
	return fmt.Sprintf("payment: attempted %s exceeds outstanding %s", e.Attempted, e.Outstanding)
}

// IdempotencyConflictError means the idempotency key was already used
// for a different debt or amount — a genuine conflict, not the safe
// replay spec §15/§31 describes.
type IdempotencyConflictError struct {
	Existing Payment
}

func (e *IdempotencyConflictError) Error() string {
	return fmt.Sprintf("payment: idempotency key already used for payment %d", e.Existing.ID)
}

const paymentsIdempotencyKeyConstraint = "payments_idempotency_key_key"

type Result struct {
	Payment  Payment
	Debt     debt.Debt
	Replayed bool // true if this idempotency key had already been recorded
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Record validates and applies a payment against a debt (spec §13-15,
// §32-34):
//
//  1. A pre-check for the idempotency key outside any lock — the common
//     case (a fresh key) skips straight to step 2 without contending
//     for the debt row.
//  2. Inside db.WithTx, the debt row is locked FOR UPDATE so concurrent
//     Record calls against the same debt serialize: only the caller
//     holding the lock sees a consistent outstanding balance, and every
//     other caller blocks until it commits or rolls back.
//  3. The payment is validated against that locked balance and, if
//     valid, written along with its ledger entries and the debt's new
//     status in the same transaction.
//
// A second caller that raced on the exact same idempotency key (rather
// than a distinct payment) hits the payments table's unique constraint
// instead of a stale balance check — that path is handled after the
// transaction rolls back, by re-reading the now-committed row.
func (s *Service) Record(ctx context.Context, userID, debtID int64, amount money.Money, idempotencyKey string) (Result, error) {
	if idempotencyKey == "" {
		return Result{}, ErrIdempotencyKeyRequired
	}
	if amount.IsZero() || amount.IsNegative() {
		return Result{}, ErrInvalidAmount
	}

	if existing, err := GetByIdempotencyKey(ctx, s.pool, idempotencyKey); err == nil {
		return s.replay(ctx, userID, debtID, amount, existing)
	} else if !errors.Is(err, ErrNotFound) {
		return Result{}, err
	}

	var result Result
	txErr := db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		r, err := recordLocked(ctx, tx, userID, debtID, amount, idempotencyKey)
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if txErr == nil {
		return result, nil
	}

	if db.IsUniqueViolation(txErr, paymentsIdempotencyKeyConstraint) {
		existing, err := GetByIdempotencyKey(ctx, s.pool, idempotencyKey)
		if err != nil {
			return Result{}, txErr
		}
		return s.replay(ctx, userID, debtID, amount, existing)
	}
	return Result{}, txErr
}

// replay handles an idempotency key that already has a recorded payment
// (spec §15/§31): same debt and amount is a safe replay, anything else
// is a conflict the caller must resolve explicitly.
func (s *Service) replay(ctx context.Context, userID, debtID int64, amount money.Money, existing Payment) (Result, error) {
	if existing.DebtID != debtID || existing.AmountMinor != amount.MinorUnits() {
		return Result{}, &IdempotencyConflictError{Existing: existing}
	}
	d, err := debt.GetByID(ctx, s.pool, userID, debtID)
	if err != nil {
		return Result{}, err
	}
	return Result{Payment: existing, Debt: d, Replayed: true}, nil
}

func recordLocked(ctx context.Context, tx pgx.Tx, userID, debtID int64, amount money.Money, idempotencyKey string) (Result, error) {
	d, err := debt.GetForUpdate(ctx, tx, userID, debtID)
	if err != nil {
		return Result{}, err
	}
	if d.Status == debt.StatusSettled {
		return Result{}, ErrDebtSettled
	}
	if amount.Currency() != d.Amount.Currency() {
		return Result{}, money.ErrCurrencyMismatch
	}

	paidMinor, err := SumByDebt(ctx, tx, debtID)
	if err != nil {
		return Result{}, err
	}
	outstandingMinor := d.Amount.MinorUnits() - paidMinor
	if amount.MinorUnits() > outstandingMinor {
		return Result{}, &OverpaymentError{
			Outstanding: money.New(outstandingMinor, d.Amount.Currency()),
			Attempted:   amount,
		}
	}

	p, err := Insert(ctx, tx, Payment{
		DebtID:         debtID,
		AmountMinor:    amount.MinorUnits(),
		Currency:       string(amount.Currency()),
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return Result{}, err
	}

	newStatus := debt.StatusPartiallyPaid
	if outstandingMinor-amount.MinorUnits() == 0 {
		newStatus = debt.StatusSettled
	}
	if !d.Status.CanTransitionTo(newStatus) {
		return Result{}, fmt.Errorf("payment: invalid debt transition %s -> %s", d.Status, newStatus)
	}
	if err := debt.UpdateStatus(ctx, tx, debtID, newStatus); err != nil {
		return Result{}, err
	}

	ref := idempotencyKey
	if _, err := ledger.Insert(ctx, tx, ledger.Entry{
		UserID:      userID,
		DebtID:      &debtID,
		Type:        ledger.EntryPaymentRecorded,
		AmountMinor: -amount.MinorUnits(),
		Currency:    string(amount.Currency()),
		Reference:   &ref,
	}); err != nil {
		return Result{}, err
	}

	if newStatus == debt.StatusSettled {
		if _, err := ledger.Insert(ctx, tx, ledger.Entry{
			UserID:      userID,
			DebtID:      &debtID,
			Type:        ledger.EntryDebtSettled,
			AmountMinor: 0,
			Currency:    string(amount.Currency()),
		}); err != nil {
			return Result{}, err
		}
	}

	d.Status = newStatus
	return Result{Payment: p, Debt: d}, nil
}
