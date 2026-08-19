package ai_test

import (
	"context"
	"testing"
	"time"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/dbtest"
)

func TestPendingAction_SetGetClear(t *testing.T) {
	rdb := dbtest.OpenRedis(t)
	ctx := context.Background()
	const userID = int64(101)

	if _, ok, err := ai.GetPendingAction(ctx, rdb, userID); err != nil || ok {
		t.Fatalf("got ok=%v err=%v before Set, want ok=false err=nil", ok, err)
	}

	pending := ai.PendingAction{
		Kind: ai.PendingConfirm,
		Intent: ai.RawIntent{
			Intent:       ai.IntentCreateDebt,
			CustomerName: new("Chinedu"),
			AmountMinor:  new(int64(7500000)),
			Confidence:   ai.ConfidenceLow,
			Language:     ai.LangPidgin,
		},
	}
	if err := ai.SetPendingAction(ctx, rdb, userID, pending, ai.DefaultPendingTTL); err != nil {
		t.Fatalf("SetPendingAction: %v", err)
	}

	got, ok, err := ai.GetPendingAction(ctx, rdb, userID)
	if err != nil {
		t.Fatalf("GetPendingAction: %v", err)
	}
	if !ok {
		t.Fatal("got ok=false after Set, want true")
	}
	if got.Kind != ai.PendingConfirm || *got.Intent.CustomerName != "Chinedu" {
		t.Fatalf("got %+v, want a round-trip of the stored pending action", got)
	}

	if err := ai.ClearPendingAction(ctx, rdb, userID); err != nil {
		t.Fatalf("ClearPendingAction: %v", err)
	}
	if _, ok, err := ai.GetPendingAction(ctx, rdb, userID); err != nil || ok {
		t.Fatalf("got ok=%v err=%v after Clear, want ok=false err=nil", ok, err)
	}
}

func TestPendingAction_TTLExpiry(t *testing.T) {
	rdb := dbtest.OpenRedis(t)
	ctx := context.Background()
	const userID = int64(102)

	pending := ai.PendingAction{Kind: ai.PendingDisambiguateCustomer, Intent: ai.RawIntent{Intent: ai.IntentRecordPayment}}
	if err := ai.SetPendingAction(ctx, rdb, userID, pending, 50*time.Millisecond); err != nil {
		t.Fatalf("SetPendingAction: %v", err)
	}

	time.Sleep(150 * time.Millisecond)

	if _, ok, err := ai.GetPendingAction(ctx, rdb, userID); err != nil || ok {
		t.Fatalf("got ok=%v err=%v after TTL expiry, want ok=false err=nil", ok, err)
	}
}
