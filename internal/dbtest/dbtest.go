// Package dbtest provides a shared Postgres pool for integration tests
// across internal packages. Slice 1's concurrency and locking guarantees
// (spec §33/§34) can only be proven against a real database, so these
// are integration tests, not mocks.
package dbtest

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Matches the docker-compose.yml / .env.example local dev default — not
// a real secret.
const defaultTestDatabaseURL = "postgres://ruby:ruby@localhost:5432/ruby?sslmode=disable" //nolint:gosec

// Open returns a pool for DATABASE_URL (falling back to the local
// docker-compose default), skipping the test if no database is
// reachable. Callers are expected to have already run the migrations
// (make migrate-up locally, the CI workflow in .github/workflows/ci.yml).
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = defaultTestDatabaseURL
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Skipf("dbtest: cannot create pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("dbtest: database not reachable: %v", err)
	}
	t.Cleanup(pool.Close)

	Truncate(t, pool)
	return pool
}

// Truncate clears every domain table so each test starts from a clean
// slate. RESTART IDENTITY keeps generated IDs predictable across tests.
func Truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		TRUNCATE TABLE ledger_entries, payments, reminders, messages, debts, customers, users
		RESTART IDENTITY CASCADE
	`)
	if err != nil {
		t.Fatalf("dbtest: truncate failed: %v", err)
	}
}

// CreateUser inserts a minimal user row and returns its id. The users
// table isn't owned by any Slice 1 package (account/auth is out of
// scope), so fixtures live here rather than in a domain package.
func CreateUser(t *testing.T, pool *pgxpool.Pool, phone string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (name, phone_number, business_name) VALUES ($1, $2, $3) RETURNING id
	`, "Test Trader", phone, "Test Trading").Scan(&id)
	if err != nil {
		t.Fatalf("dbtest: create user failed: %v", err)
	}
	return id
}
