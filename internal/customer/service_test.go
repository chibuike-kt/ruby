package customer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

func strPtr(s string) *string { return &s }

func TestCreate(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000001")
	svc := customer.NewService(pool)

	c, err := svc.Create(context.Background(), userID, "  Chinedu  ", strPtr("0803XXX1234"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Name != "Chinedu" {
		t.Fatalf("got name %q, want trimmed %q", c.Name, "Chinedu")
	}
	if c.ID == 0 {
		t.Fatal("expected a generated id")
	}
}

func TestCreate_EmptyNameRejected(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000002")
	svc := customer.NewService(pool)

	_, err := svc.Create(context.Background(), userID, "   ", nil, nil)
	if !errors.Is(err, customer.ErrInvalidName) {
		t.Fatalf("got %v, want ErrInvalidName", err)
	}
}

func TestGetByID_CrossUserAccessDenied(t *testing.T) {
	pool := dbtest.Open(t)
	ownerID := dbtest.CreateUser(t, pool, "+2348010000003")
	otherID := dbtest.CreateUser(t, pool, "+2348010000004")
	svc := customer.NewService(pool)
	ctx := context.Background()

	c, err := svc.Create(ctx, ownerID, "Ngozi", nil, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err = customer.GetByID(ctx, pool, otherID, c.ID)
	if !errors.Is(err, customer.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound for cross-user access (spec §35)", err)
	}
}

func TestResolve_ByID(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000005")
	svc := customer.NewService(pool)
	ctx := context.Background()

	c, err := svc.Create(ctx, userID, "Ada", nil, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := svc.Resolve(ctx, userID, customer.Ref{CustomerID: &c.ID})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != c.ID {
		t.Fatalf("got id %d, want %d", got.ID, c.ID)
	}
}

func TestResolve_ByPhone_Unambiguous(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000006")
	svc := customer.NewService(pool)
	ctx := context.Background()

	c, err := svc.Create(ctx, userID, "Chinedu", strPtr("0803XXX1234"), nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := svc.Create(ctx, userID, "Chinedu", strPtr("0907XXX5678"), nil); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := svc.Resolve(ctx, userID, customer.Ref{Phone: strPtr("0803XXX1234")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != c.ID {
		t.Fatalf("phone lookup resolved to id %d, want %d", got.ID, c.ID)
	}
}

// Same name, different phone — spec §7's example, and one of the
// explicit mandatory test cases in spec §45.
func TestResolve_ByName_DuplicateNames_Ambiguous(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000007")
	svc := customer.NewService(pool)
	ctx := context.Background()

	if _, err := svc.Create(ctx, userID, "Chinedu", strPtr("0803XXX1234"), nil); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := svc.Create(ctx, userID, "Chinedu", strPtr("0907XXX5678"), nil); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := svc.Resolve(ctx, userID, customer.Ref{Name: strPtr("Chinedu")})
	var ambiguous *customer.AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want AmbiguousError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(ambiguous.Candidates))
	}
}

func TestResolve_ByName_SingleMatch(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000008")
	svc := customer.NewService(pool)
	ctx := context.Background()

	c, err := svc.Create(ctx, userID, "Musa", nil, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := svc.Resolve(ctx, userID, customer.Ref{Name: strPtr("Musa")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != c.ID {
		t.Fatalf("got id %d, want %d", got.ID, c.ID)
	}
}

// Same underlying customer, resolved via alias rather than name — alias
// resolution must be unaffected by unrelated duplicate-name customers.
func TestResolve_ByAlias_DifferentFromName(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000009")
	svc := customer.NewService(pool)
	ctx := context.Background()

	target, err := svc.Create(ctx, userID, "Chinedu Okafor", nil, strPtr("Chinedu Mechanic"))
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := svc.Create(ctx, userID, "Chinedu Okafor", nil, nil); err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := svc.Resolve(ctx, userID, customer.Ref{Alias: strPtr("Chinedu Mechanic")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != target.ID {
		t.Fatalf("got id %d, want %d", got.ID, target.ID)
	}
}

func TestResolve_NotFound(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000010")
	svc := customer.NewService(pool)

	_, err := svc.Resolve(context.Background(), userID, customer.Ref{Name: strPtr("Nobody")})
	if !errors.Is(err, customer.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestResolve_NoSignalProvided(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000011")
	svc := customer.NewService(pool)

	_, err := svc.Resolve(context.Background(), userID, customer.Ref{})
	if !errors.Is(err, customer.ErrNoIdentitySignal) {
		t.Fatalf("got %v, want ErrNoIdentitySignal", err)
	}
}
