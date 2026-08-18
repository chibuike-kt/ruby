package payment

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/ruby/internal/db"
)

var ErrNotFound = errors.New("payment: not found")

func Insert(ctx context.Context, q db.Querier, p Payment) (Payment, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO payments (debt_id, amount_minor, currency, idempotency_key)
		VALUES ($1, $2, $3, $4)
		RETURNING id, debt_id, amount_minor, currency, idempotency_key, created_at
	`, p.DebtID, p.AmountMinor, p.Currency, p.IdempotencyKey)
	return scan(row)
}

func GetByIdempotencyKey(ctx context.Context, q db.Querier, key string) (Payment, error) {
	row := q.QueryRow(ctx, `
		SELECT id, debt_id, amount_minor, currency, idempotency_key, created_at
		FROM payments WHERE idempotency_key = $1
	`, key)
	p, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Payment{}, ErrNotFound
	}
	return p, err
}

// SumByDebt returns the total already paid against a debt. Called with
// tx inside the locked payment transaction, it reflects exactly the
// payments committed before the lock was acquired — see Service.Record.
func SumByDebt(ctx context.Context, q db.Querier, debtID int64) (int64, error) {
	var sum int64
	err := q.QueryRow(ctx, `SELECT COALESCE(SUM(amount_minor), 0) FROM payments WHERE debt_id = $1`, debtID).Scan(&sum)
	return sum, err
}

func ListByDebt(ctx context.Context, q db.Querier, debtID int64) ([]Payment, error) {
	rows, err := q.Query(ctx, `
		SELECT id, debt_id, amount_minor, currency, idempotency_key, created_at
		FROM payments WHERE debt_id = $1
		ORDER BY id
	`, debtID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Payment
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type row interface {
	Scan(dest ...any) error
}

func scan(r row) (Payment, error) {
	var p Payment
	err := r.Scan(&p.ID, &p.DebtID, &p.AmountMinor, &p.Currency, &p.IdempotencyKey, &p.CreatedAt)
	return p, err
}
