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
	"github.com/redis/go-redis/v9"
)

// Matches the docker-compose.yml / .env.example local dev default — not
// a real secret.
const defaultTestDatabaseURL = "postgres://ruby:ruby@localhost:5432/ruby?sslmode=disable" //nolint:gosec
const defaultTestRedisURL = "redis://localhost:6379/0"

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
// table isn't owned by any Slice 1 domain package (internal/account only
// reads it, for auth resolution), so fixtures live here instead.
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

// InsertMessage inserts a raw row into the messages table, bypassing any
// service layer — no package owns webhook message persistence yet
// (that's Slice 2's internal/whatsapp). It's used to exercise the
// provider_message_id unique index directly, e.g. to prove Postgres
// still catches a duplicate event when Redis's dedup fast-path misses.
func InsertMessage(t *testing.T, pool *pgxpool.Pool, userID int64, providerMessageID string) error {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO messages (user_id, provider_message_id, direction, message_type)
		VALUES ($1, $2, 'INBOUND', 'TEXT')
	`, userID, providerMessageID)
	return err
}

// OpenRedis returns a client for REDIS_URL (falling back to the local
// docker-compose default), skipping the test if no Redis is reachable.
// FlushDB gives each test a clean keyspace — Slice 1's Truncate resets
// Postgres identity sequences per test, so without a matching flush,
// Redis keys built from a reused user_id (e.g. rate-limit or dedup
// counters) could leak state between otherwise-isolated tests.
func OpenRedis(t *testing.T) *redis.Client {
	t.Helper()

	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = defaultTestRedisURL
	}

	opt, err := redis.ParseURL(url)
	if err != nil {
		t.Skipf("dbtest: invalid REDIS_URL: %v", err)
	}

	ctx := context.Background()
	client := redis.NewClient(opt)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		t.Skipf("dbtest: redis not reachable: %v", err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		_ = client.Close()
		t.Fatalf("dbtest: redis flushdb failed: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	return client
}
