package debt

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/ledger"
	"github.com/chibuike-kt/ruby/internal/money"
)

var ErrInvalidAmount = errors.New("debt: amount must be positive")

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Create records a new debt (spec §12) and its DEBT_CREATED ledger
// entry in one transaction, after confirming the customer belongs to
// this trader (spec §35).
func (s *Service) Create(ctx context.Context, userID, customerID int64, amount money.Money, description string, dueDate *time.Time) (Debt, error) {
	if amount.IsZero() || amount.IsNegative() {
		return Debt{}, ErrInvalidAmount
	}
	if _, err := customer.GetByID(ctx, s.pool, userID, customerID); err != nil {
		return Debt{}, err
	}

	var created Debt
	err := db.WithTx(ctx, s.pool, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		created, err = CreateWithLedger(ctx, tx, Debt{
			UserID:      userID,
			CustomerID:  customerID,
			Amount:      amount,
			Description: strings.TrimSpace(description),
			DueDate:     dueDate,
			Status:      StatusOutstanding,
		})
		return err
	})
	if err != nil {
		return Debt{}, err
	}
	return created, nil
}

// CreateWithLedger inserts a debt and its DEBT_CREATED ledger entry
// against whatever db.Querier it's given — Service.Create wraps this in
// its own transaction for standalone use; a caller that needs the debt
// write to be atomic with something else (e.g. internal/ai's
// customer-then-debt sequence, docs/BRIEF-critical-fixes-and-
// reminders.md #1b) passes its own tx instead, so a failure anywhere in
// that wider sequence rolls back the debt (and ledger entry) too, not
// just the part debt.Service happened to wrap itself.
func CreateWithLedger(ctx context.Context, q db.Querier, d Debt) (Debt, error) {
	created, err := Create(ctx, q, d)
	if err != nil {
		return Debt{}, err
	}
	if _, err := ledger.Insert(ctx, q, ledger.Entry{
		UserID:      d.UserID,
		DebtID:      &created.ID,
		Type:        ledger.EntryDebtCreated,
		AmountMinor: d.Amount.MinorUnits(),
		Currency:    string(d.Amount.Currency()),
	}); err != nil {
		return Debt{}, err
	}
	return created, nil
}

func (s *Service) Get(ctx context.Context, userID, id int64) (Debt, error) {
	return GetByID(ctx, s.pool, userID, id)
}

func (s *Service) ListOutstanding(ctx context.Context, userID int64) ([]Debt, error) {
	return ListOutstandingByUser(ctx, s.pool, userID)
}

// ListByCustomer returns a customer's full debt history (any status)
// — docs/BRIEF-disambiguation-reminders-statements.md Tier 3.
func (s *Service) ListByCustomer(ctx context.Context, userID, customerID int64) ([]Debt, error) {
	return ListByCustomer(ctx, s.pool, userID, customerID)
}
