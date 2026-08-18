// Package db provides shared PostgreSQL access: a pgx pool constructor and
// a Querier interface that lets repositories run against either the pool
// or an in-flight transaction without knowing which.
package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is satisfied by both *pgxpool.Pool and pgx.Tx. Repository
// functions take it as a parameter rather than holding a connection,
// so the same function works whether it's called standalone or as one
// step inside a caller's transaction.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, databaseURL)
}

// WithTx runs fn inside a transaction, committing on success and rolling
// back on error. Financial mutations that span more than one write (spec
// §32) must go through this rather than issuing BEGIN/COMMIT by hand.
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// IsUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505), optionally narrowed to a specific
// constraint name. Callers use this to distinguish a genuine failure
// from a benign race against a concurrent insert under the same key.
func IsUniqueViolation(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != "23505" {
		return false
	}
	return constraint == "" || pgErr.ConstraintName == constraint
}
