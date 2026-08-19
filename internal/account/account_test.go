package account_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chibuike-kt/ruby/internal/account"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

func TestGetByID(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348110000101")

	a, err := account.GetByID(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.PhoneNumber != "+2348110000101" {
		t.Fatalf("got phone %q, want +2348110000101", a.PhoneNumber)
	}
}

func TestGetByID_NotFound(t *testing.T) {
	pool := dbtest.Open(t)

	_, err := account.GetByID(context.Background(), pool, 999999)
	if !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestGetByPhoneNumber(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348110000102")

	a, err := account.GetByPhoneNumber(context.Background(), pool, "+2348110000102")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.ID != userID {
		t.Fatalf("got id %d, want %d", a.ID, userID)
	}
}

func TestGetByPhoneNumber_NotFound(t *testing.T) {
	pool := dbtest.Open(t)

	_, err := account.GetByPhoneNumber(context.Background(), pool, "+2340000000000")
	if !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// The exact format users.phone_number is stored in must match — this
// package does no normalization itself, callers own that.
func TestGetByPhoneNumber_ExactFormatRequired(t *testing.T) {
	pool := dbtest.Open(t)
	dbtest.CreateUser(t, pool, "+2348110000103")

	_, err := account.GetByPhoneNumber(context.Background(), pool, "2348110000103") // missing leading +
	if !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound for a differently-formatted number", err)
	}
}
