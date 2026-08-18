// Package account provides read access to the users table (spec §4).
// It exists for the API layer's temporary auth middleware to resolve a
// caller-supplied user id against a real account — full account
// management (recovery, phone-number change, spec §26/§27) is out of
// scope for this slice.
package account

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/chibuike-kt/ruby/internal/db"
)

var ErrNotFound = errors.New("account: not found")

type Account struct {
	ID           int64
	Name         string
	PhoneNumber  string
	BusinessName *string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func GetByID(ctx context.Context, q db.Querier, id int64) (Account, error) {
	row := q.QueryRow(ctx, `
		SELECT id, name, phone_number, business_name, created_at, updated_at
		FROM users WHERE id = $1
	`, id)

	var a Account
	err := row.Scan(&a.ID, &a.Name, &a.PhoneNumber, &a.BusinessName, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}
