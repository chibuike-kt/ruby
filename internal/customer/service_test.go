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

// decisions.md #8: prevent an indistinguishable duplicate at creation
// time rather than only resolving it later.
func TestCreate_DuplicateName_NoSignal_Rejected(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000012")
	svc := customer.NewService(pool)
	ctx := context.Background()

	if _, err := svc.Create(ctx, userID, "Chinedu", nil, nil); err != nil {
		t.Fatalf("first create: unexpected error: %v", err)
	}

	_, err := svc.Create(ctx, userID, "Chinedu", nil, nil)
	if !errors.Is(err, customer.ErrDuplicateNameRequiresSignal) {
		t.Fatalf("got %v, want ErrDuplicateNameRequiresSignal", err)
	}
}

func TestCreate_DuplicateName_BlankPhoneTreatedAsMissing(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000013")
	svc := customer.NewService(pool)
	ctx := context.Background()

	if _, err := svc.Create(ctx, userID, "Chinedu", nil, nil); err != nil {
		t.Fatalf("first create: unexpected error: %v", err)
	}

	_, err := svc.Create(ctx, userID, "Chinedu", strPtr("   "), nil)
	if !errors.Is(err, customer.ErrDuplicateNameRequiresSignal) {
		t.Fatalf("got %v, want ErrDuplicateNameRequiresSignal for a blank phone", err)
	}
}

func TestCreate_DuplicateName_WithPhone_Allowed(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000014")
	svc := customer.NewService(pool)
	ctx := context.Background()

	if _, err := svc.Create(ctx, userID, "Chinedu", nil, nil); err != nil {
		t.Fatalf("first create: unexpected error: %v", err)
	}

	c, err := svc.Create(ctx, userID, "Chinedu", strPtr("0803XXX1234"), nil)
	if err != nil {
		t.Fatalf("second create with phone: unexpected error: %v", err)
	}
	if c.PhoneNumber == nil || *c.PhoneNumber != "0803XXX1234" {
		t.Fatalf("got phone %v, want 0803XXX1234", c.PhoneNumber)
	}
}

func TestCreate_DuplicateName_WithAlias_Allowed(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000015")
	svc := customer.NewService(pool)
	ctx := context.Background()

	if _, err := svc.Create(ctx, userID, "Chinedu", nil, nil); err != nil {
		t.Fatalf("first create: unexpected error: %v", err)
	}

	if _, err := svc.Create(ctx, userID, "Chinedu", nil, strPtr("Chinedu Mechanic")); err != nil {
		t.Fatalf("second create with alias: unexpected error: %v", err)
	}
}

// TestCreate_DuplicateName_CaseInsensitive_Rejected is docs/BRIEF-
// disambiguation-reminders-statements.md Tier 1a's regression test —
// the actual transcript: a second "Emmanuel" (crocs, 50k) got created
// with no alias/phone alongside an existing "Emmanuel" (pure water,
// 300) because the guard's own lookup was case-sensitive and the two
// mentions weren't typed with identical capitalization.
func TestCreate_DuplicateName_CaseInsensitive_Rejected(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000019")
	svc := customer.NewService(pool)
	ctx := context.Background()

	if _, err := svc.Create(ctx, userID, "Emmanuel", nil, nil); err != nil {
		t.Fatalf("first create: unexpected error: %v", err)
	}

	_, err := svc.Create(ctx, userID, "emmanuel", nil, nil)
	if !errors.Is(err, customer.ErrDuplicateNameRequiresSignal) {
		t.Fatalf("got %v, want ErrDuplicateNameRequiresSignal for \"emmanuel\" against an existing \"Emmanuel\"", err)
	}
}

func TestCreate_DuplicateName_ScopedPerUser(t *testing.T) {
	pool := dbtest.Open(t)
	userA := dbtest.CreateUser(t, pool, "+2348010000016")
	userB := dbtest.CreateUser(t, pool, "+2348010000017")
	svc := customer.NewService(pool)
	ctx := context.Background()

	if _, err := svc.Create(ctx, userA, "Chinedu", nil, nil); err != nil {
		t.Fatalf("user A create: unexpected error: %v", err)
	}
	if _, err := svc.Create(ctx, userB, "Chinedu", nil, nil); err != nil {
		t.Fatalf("user B create: unexpected error: %v — the duplicate check must be scoped per user", err)
	}
}

// True worst case (decisions.md #8): two pre-existing duplicate
// customers — no phone, no alias, identical debt description — the
// only way this state can occur once Create rejects it up front is
// legacy data, so this is seeded via the repository function directly
// rather than through Service.Create.
func TestResolve_ByName_TrueWorstCase_FallsBackToCreationOrder(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000018")
	svc := customer.NewService(pool)
	ctx := context.Background()

	first, err := customer.Create(ctx, pool, customer.Customer{UserID: userID, Name: "Chinedu"})
	if err != nil {
		t.Fatalf("setup first: %v", err)
	}
	second, err := customer.Create(ctx, pool, customer.Customer{UserID: userID, Name: "Chinedu"})
	if err != nil {
		t.Fatalf("setup second: %v", err)
	}

	_, err = svc.Resolve(ctx, userID, customer.Ref{Name: strPtr("Chinedu")})
	var ambiguous *customer.AmbiguousError
	if !errors.As(err, &ambiguous) {
		t.Fatalf("got %v, want AmbiguousError", err)
	}
	if len(ambiguous.Candidates) != 2 {
		t.Fatalf("got %d candidates, want 2", len(ambiguous.Candidates))
	}

	// Same description on both sides, and neither has a phone or
	// alias — no signal but creation order can tell them apart.
	hints := ambiguous.Hints(map[int64]string{
		first.ID:  "3 bags of rice",
		second.ID: "3 bags of rice",
	})

	for _, h := range hints {
		if h.Hint == "" {
			t.Fatal("got an empty hint in the true worst case, want a creation-order fallback")
		}
		if h.Hint == "3 bags of rice" {
			t.Fatal("identical descriptions must not be used as a tiebreaker (decisions.md #8)")
		}
	}
	if hints[0].Customer.ID == hints[1].Customer.ID {
		t.Fatal("candidates must remain individually distinguishable even in the worst case")
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

// TestResolve_ByName_CaseInsensitive_Matches is the other half of Tier
// 1a/1b's root cause: a bare-name reference must match an existing
// customer regardless of capitalization, the same requirement
// TestCreate_DuplicateName_CaseInsensitive_Rejected exercises at
// creation time.
func TestResolve_ByName_CaseInsensitive_Matches(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348010000020")
	svc := customer.NewService(pool)
	ctx := context.Background()

	c, err := svc.Create(ctx, userID, "Emmanuel", nil, nil)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	got, err := svc.Resolve(ctx, userID, customer.Ref{Name: strPtr("emmanuel")})
	if err != nil {
		t.Fatalf("unexpected error resolving \"emmanuel\" against stored \"Emmanuel\": %v", err)
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
	if _, err := svc.Create(ctx, userID, "Chinedu Okafor", strPtr("0803XXX1234"), nil); err != nil {
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
