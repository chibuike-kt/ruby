package idempotency_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/idempotency"
)

func TestCheckAndMark_SecondCallDetected(t *testing.T) {
	rdb := dbtest.OpenRedis(t)
	ctx := context.Background()

	seenBefore, err := idempotency.CheckAndMark(ctx, rdb, "wamid.first", time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenBefore {
		t.Fatal("first call: got seenBefore=true, want false")
	}

	seenBefore, err = idempotency.CheckAndMark(ctx, rdb, "wamid.first", time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !seenBefore {
		t.Fatal("second call with the same provider message id: got seenBefore=false, want true")
	}
}

func TestCheckAndMark_DistinctIDsIndependent(t *testing.T) {
	rdb := dbtest.OpenRedis(t)
	ctx := context.Background()

	if _, err := idempotency.CheckAndMark(ctx, rdb, "wamid.a", time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	seenBefore, err := idempotency.CheckAndMark(ctx, rdb, "wamid.b", time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seenBefore {
		t.Fatal("a different provider message id should not be seen as a duplicate")
	}
}

func TestCheckAndMark_EmptyIDRejected(t *testing.T) {
	rdb := dbtest.OpenRedis(t)

	_, err := idempotency.CheckAndMark(context.Background(), rdb, "", time.Minute)
	if !errors.Is(err, idempotency.ErrProviderMessageIDRequired) {
		t.Fatalf("got %v, want ErrProviderMessageIDRequired", err)
	}
}

// Spec §15/§31, decisions.md #2: Redis is an optimization only. This
// proves the actual backstop — the unique index on
// messages.provider_message_id — still catches a duplicate event even
// when the Redis fast-path never saw it (down, evicted, or simply never
// called), independent of any Go service code.
func TestRedisMiss_PostgresUniqueIndexStillCatchesDuplicate(t *testing.T) {
	pool := dbtest.Open(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000001")

	const providerMessageID = "wamid.never-marked-in-redis"

	if err := dbtest.InsertMessage(t, pool, userID, providerMessageID); err != nil {
		t.Fatalf("first insert: unexpected error: %v", err)
	}

	err := dbtest.InsertMessage(t, pool, userID, providerMessageID)
	if err == nil {
		t.Fatal("second insert with the same provider_message_id: got nil error, want a unique-violation")
	}
}
