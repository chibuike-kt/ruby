// Package account provides access to the users table (spec §4): reads
// for the API layer's temporary auth middleware, and the writes
// auto-onboarding needs (Create on first contact, SetName once a
// trader's name is captured — docs/BRIEF-response-quality.md #1). Full
// account management (recovery, phone-number change, spec §26/§27) is
// still out of scope for this slice.
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
	return scan(row)
}

// GetByPhoneNumber resolves a trader by their WhatsApp number (spec
// §4 — the number is a lookup key, never the permanent identity).
// Callers are responsible for normalizing phone into the same format
// users.phone_number is stored in (E.164 with a leading "+"); this
// function does no normalization of its own.
func GetByPhoneNumber(ctx context.Context, q db.Querier, phone string) (Account, error) {
	row := q.QueryRow(ctx, `
		SELECT id, name, phone_number, business_name, created_at, updated_at
		FROM users WHERE phone_number = $1
	`, phone)
	return scan(row)
}

// Create inserts a new user row with an empty name — auto-onboarding
// (docs/BRIEF-response-quality.md #1): Ruby never guesses a trader's
// name from their WhatsApp profile, it asks, and the phone number is
// claimed immediately so idempotency/dedup work normally from the
// first message. name is filled in later via SetName.
func Create(ctx context.Context, q db.Querier, phone string) (Account, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO users (name, phone_number) VALUES ('', $1)
		RETURNING id, name, phone_number, business_name, created_at, updated_at
	`, phone)
	return scan(row)
}

// SetName saves a trader's captured name (or a placeholder — see
// internal/ai's name-capture flow) once the auto-onboarding question is
// answered.
func SetName(ctx context.Context, q db.Querier, id int64, name string) error {
	_, err := q.Exec(ctx, `UPDATE users SET name = $1, updated_at = now() WHERE id = $2`, name, id)
	return err
}

func scan(row pgx.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Name, &a.PhoneNumber, &a.BusinessName, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrNotFound
	}
	return a, err
}
