package customer_test

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

func TestLastCustomerContext_SetThenGet(t *testing.T) {
	rdb := dbtest.OpenRedis(t)
	ctx := context.Background()

	if err := customer.SetLastCustomerContext(ctx, rdb, 42, 1042, customer.DefaultLastCustomerContextTTL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok, err := customer.GetLastCustomerContext(ctx, rdb, 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("got ok=false, want true")
	}
	if got != 1042 {
		t.Fatalf("got customer id %d, want 1042", got)
	}
}

func TestLastCustomerContext_MissingKey(t *testing.T) {
	rdb := dbtest.OpenRedis(t)

	_, ok, err := customer.GetLastCustomerContext(context.Background(), rdb, 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("got ok=true for a user with no stored context, want false")
	}
}

func TestLastCustomerContext_ScopedByUser(t *testing.T) {
	rdb := dbtest.OpenRedis(t)
	ctx := context.Background()

	if err := customer.SetLastCustomerContext(ctx, rdb, 1, 100, customer.DefaultLastCustomerContextTTL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, ok, err := customer.GetLastCustomerContext(ctx, rdb, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("a different user's context must not be visible")
	}
}

func TestLastCustomerContext_TTLExpiry(t *testing.T) {
	rdb := dbtest.OpenRedis(t)
	ctx := context.Background()

	const shortTTL = 50 * time.Millisecond
	if err := customer.SetLastCustomerContext(ctx, rdb, 7, 700, shortTTL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	time.Sleep(shortTTL + 100*time.Millisecond)

	_, ok, err := customer.GetLastCustomerContext(ctx, rdb, 7)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("got ok=true after TTL expiry, want false")
	}
}
