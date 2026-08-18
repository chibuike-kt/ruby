package ledger

import (
	"context"

	"github.com/chibuike-kt/ruby/internal/db"
)

func Insert(ctx context.Context, q db.Querier, e Entry) (Entry, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO ledger_entries (user_id, debt_id, type, amount_minor, currency, reference)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, debt_id, type, amount_minor, currency, reference, created_at
	`, e.UserID, e.DebtID, string(e.Type), e.AmountMinor, e.Currency, e.Reference)
	return scan(row)
}

func ListByDebt(ctx context.Context, q db.Querier, debtID int64) ([]Entry, error) {
	return list(ctx, q, `
		SELECT id, user_id, debt_id, type, amount_minor, currency, reference, created_at
		FROM ledger_entries WHERE debt_id = $1
		ORDER BY id
	`, debtID)
}

func ListByUser(ctx context.Context, q db.Querier, userID int64) ([]Entry, error) {
	return list(ctx, q, `
		SELECT id, user_id, debt_id, type, amount_minor, currency, reference, created_at
		FROM ledger_entries WHERE user_id = $1
		ORDER BY id
	`, userID)
}

func list(ctx context.Context, q db.Querier, sql string, args ...any) ([]Entry, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type row interface {
	Scan(dest ...any) error
}

func scan(r row) (Entry, error) {
	var e Entry
	var entryType string
	err := r.Scan(&e.ID, &e.UserID, &e.DebtID, &entryType, &e.AmountMinor, &e.Currency, &e.Reference, &e.CreatedAt)
	e.Type = EntryType(entryType)
	return e, err
}
