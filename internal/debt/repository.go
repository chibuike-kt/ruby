package debt

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/money"
)

var ErrNotFound = errors.New("debt: not found")

func Create(ctx context.Context, q db.Querier, d Debt) (Debt, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO debts (user_id, customer_id, amount_minor, currency, description, due_date, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, user_id, customer_id, amount_minor, currency, description, due_date, status, created_at, updated_at
	`, d.UserID, d.CustomerID, d.Amount.MinorUnits(), string(d.Amount.Currency()), d.Description, d.DueDate, string(d.Status))
	return scan(row)
}

// GetByID scopes the lookup by user_id (spec §35 cross-user protection).
func GetByID(ctx context.Context, q db.Querier, userID, id int64) (Debt, error) {
	row := q.QueryRow(ctx, `
		SELECT id, user_id, customer_id, amount_minor, currency, description, due_date, status, created_at, updated_at
		FROM debts WHERE id = $1 AND user_id = $2
	`, id, userID)
	d, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Debt{}, ErrNotFound
	}
	return d, err
}

// GetForUpdate locks the debt row for the rest of the caller's
// transaction (spec §33/§34, decisions.md #3), so concurrent payments
// against the same debt serialize instead of racing on a stale read.
// It must be called with a pgx.Tx obtained from db.WithTx — FOR UPDATE
// has no effect once the statement's implicit transaction ends.
func GetForUpdate(ctx context.Context, tx db.Querier, userID, id int64) (Debt, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, customer_id, amount_minor, currency, description, due_date, status, created_at, updated_at
		FROM debts WHERE id = $1 AND user_id = $2
		FOR UPDATE
	`, id, userID)
	d, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Debt{}, ErrNotFound
	}
	return d, err
}

// ListByCustomer returns every debt for a customer — any status,
// oldest first — for docs/BRIEF-disambiguation-reminders-
// statements.md Tier 3's GET_CUSTOMER_STATEMENT: a real account
// history, not just what's currently outstanding (that's already
// ListOutstandingByUser's job).
func ListByCustomer(ctx context.Context, q db.Querier, userID, customerID int64) ([]Debt, error) {
	rows, err := q.Query(ctx, `
		SELECT id, user_id, customer_id, amount_minor, currency, description, due_date, status, created_at, updated_at
		FROM debts WHERE user_id = $1 AND customer_id = $2
		ORDER BY id
	`, userID, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Debt
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func UpdateStatus(ctx context.Context, tx db.Querier, id int64, status Status) error {
	_, err := tx.Exec(ctx, `UPDATE debts SET status = $1, updated_at = now() WHERE id = $2`, string(status), id)
	return err
}

func ListOutstandingByUser(ctx context.Context, q db.Querier, userID int64) ([]Debt, error) {
	rows, err := q.Query(ctx, `
		SELECT id, user_id, customer_id, amount_minor, currency, description, due_date, status, created_at, updated_at
		FROM debts WHERE user_id = $1 AND status != $2
		ORDER BY id
	`, userID, string(StatusSettled))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Debt
	for rows.Next() {
		d, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type row interface {
	Scan(dest ...any) error
}

func scan(r row) (Debt, error) {
	var d Debt
	var amountMinor int64
	var currency, status string
	err := r.Scan(&d.ID, &d.UserID, &d.CustomerID, &amountMinor, &currency,
		&d.Description, &d.DueDate, &status, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return Debt{}, err
	}
	d.Amount = money.New(amountMinor, money.Currency(currency))
	d.Status = Status(status)
	return d, nil
}
