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

// TestSetCreditProfileSharing covers docs/wema-integration.md's opt-in
// flag: off by default, explicit opt-in, and revocable.
func TestSetCreditProfileSharing(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348110000104")

	initial, err := account.GetByID(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if initial.CreditProfileSharingEnabled {
		t.Fatal("got sharing enabled by default, want off")
	}

	if err := account.SetCreditProfileSharing(context.Background(), pool, userID, true); err != nil {
		t.Fatalf("SetCreditProfileSharing(true): %v", err)
	}
	enabled, err := account.GetByID(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !enabled.CreditProfileSharingEnabled {
		t.Fatal("got sharing still disabled after opting in")
	}

	if err := account.SetCreditProfileSharing(context.Background(), pool, userID, false); err != nil {
		t.Fatalf("SetCreditProfileSharing(false): %v", err)
	}
	revoked, err := account.GetByID(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if revoked.CreditProfileSharingEnabled {
		t.Fatal("got sharing still enabled after revoking — consent must be revocable")
	}
}
