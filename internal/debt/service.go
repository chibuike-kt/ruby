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
		d, err := Create(ctx, tx, Debt{
			UserID:      userID,
			CustomerID:  customerID,
			Amount:      amount,
			Description: strings.TrimSpace(description),
			DueDate:     dueDate,
			Status:      StatusOutstanding,
		})
		if err != nil {
			return err
		}

		if _, err := ledger.Insert(ctx, tx, ledger.Entry{
			UserID:      userID,
			DebtID:      &d.ID,
			Type:        ledger.EntryDebtCreated,
			AmountMinor: amount.MinorUnits(),
			Currency:    string(amount.Currency()),
		}); err != nil {
			return err
		}

		created = d
		return nil
	})
	if err != nil {
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
