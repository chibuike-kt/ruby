package customer

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/ruby/internal/db"
)

var ErrNotFound = errors.New("customer: not found")

func Create(ctx context.Context, q db.Querier, c Customer) (Customer, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO customers (user_id, name, phone_number, alias)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, name, phone_number, alias, created_at, updated_at
	`, c.UserID, c.Name, c.PhoneNumber, c.Alias)
	return scan(row)
}

// GetByID scopes the lookup by user_id so one trader can never fetch
// another trader's customer by guessing an id (spec §35).
func GetByID(ctx context.Context, q db.Querier, userID, id int64) (Customer, error) {
	row := q.QueryRow(ctx, `
		SELECT id, user_id, name, phone_number, alias, created_at, updated_at
		FROM customers WHERE id = $1 AND user_id = $2
	`, id, userID)
	c, err := scan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Customer{}, ErrNotFound
	}
	return c, err
}

func FindByPhone(ctx context.Context, q db.Querier, userID int64, phone string) ([]Customer, error) {
	return list(ctx, q, `
		SELECT id, user_id, name, phone_number, alias, created_at, updated_at
		FROM customers WHERE user_id = $1 AND phone_number = $2
		ORDER BY id
	`, userID, phone)
}

func FindByAlias(ctx context.Context, q db.Querier, userID int64, alias string) ([]Customer, error) {
	return list(ctx, q, `
		SELECT id, user_id, name, phone_number, alias, created_at, updated_at
		FROM customers WHERE user_id = $1 AND alias = $2
		ORDER BY id
	`, userID, alias)
}

func FindByName(ctx context.Context, q db.Querier, userID int64, name string) ([]Customer, error) {
	return list(ctx, q, `
		SELECT id, user_id, name, phone_number, alias, created_at, updated_at
		FROM customers WHERE user_id = $1 AND name = $2
		ORDER BY id
	`, userID, name)
}

func ListByUser(ctx context.Context, q db.Querier, userID int64) ([]Customer, error) {
	return list(ctx, q, `
		SELECT id, user_id, name, phone_number, alias, created_at, updated_at
		FROM customers WHERE user_id = $1
		ORDER BY id
	`, userID)
}

func list(ctx context.Context, q db.Querier, sql string, args ...any) ([]Customer, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Customer
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// row is satisfied by both pgx.Row and pgx.Rows, so a single scanner
// works for QueryRow and Query result sets alike.
type row interface {
	Scan(dest ...any) error
}

func scan(r row) (Customer, error) {
	var c Customer
	err := r.Scan(&c.ID, &c.UserID, &c.Name, &c.PhoneNumber, &c.Alias, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}
