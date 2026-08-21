package whatsapp_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/payment"
	"github.com/chibuike-kt/ruby/internal/whatsapp"
)

// scriptedExtractor is a minimal ai.Extractor double — internal/ai_test's
// own fakeExtractor is unexported and unreachable from this package, and
// this test needs a real end-to-end whatsapp+ai wiring, not internal/ai
// in isolation.
type scriptedExtractor struct {
	results []ai.RawIntent
}

func (s *scriptedExtractor) Extract(_ context.Context, _ string) (ai.RawIntent, error) {
	r := s.results[0]
	if len(s.results) > 1 {
		s.results = s.results[1:]
	}
	return r, nil
}

func TestReceiveEvent_PendingConfirmSurvivesRestartAndDuplicateRedelivery(t *testing.T) {
	withStubGraphAPI(t)
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+16505559001")

	customers := customer.NewService(pool)
	debts := debt.NewService(pool)
	payments := payment.NewService(pool)

	// --- "before the deploy": first process instance handles the
	// low-confidence CREATE_DEBT and parks it pending confirmation.
	svc1 := whatsapp.NewService(pool, rdb, "test-secret", "test-verify-token", "test-access-token", "test-phone-number-id", slog.Default())
	extractor1 := &scriptedExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new(string), AmountMinor: new(int64), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
	}}
	*extractor1.results[0].CustomerName = "Chinedu"
	*extractor1.results[0].AmountMinor = 7500000

	done1 := make(chan struct{}, 4)
	proc1 := ai.NewProcessor(ai.Config{
		Extractor: extractor1,
		Sender:    svc1,
		Media:     svc1,
		Pool:      pool,
		Redis:     rdb,
		Customers: customers,
		Debts:     debts,
		Payments:  payments,
		Logger:    slog.Default(),
	})
	svc1.SetHandler(func(ctx context.Context, msg whatsapp.StoredMessage) {
		uid := int64(0)
		if msg.UserID != nil {
			uid = *msg.UserID
		}
		_, _ = proc1.Handle(ctx, ai.ToInboundMessage(uid, msg.ProviderMessageID, msg.MessageType, msg.ContentReference))
		done1 <- struct{}{}
	})

	firstPayload := textPayload("16505559001", "wamid.restart.1", "Chinedu took 75k")
	if _, err := svc1.ReceiveEvent(context.Background(), firstPayload); err != nil {
		t.Fatalf("first ReceiveEvent: %v", err)
	}
	waitFor(t, done1, "first handler completion")

	before, ok, err := ai.GetPendingAction(context.Background(), rdb, userID)
	if err != nil {
		t.Fatalf("GetPendingAction (before restart): %v", err)
	}
	if !ok || before.Kind != ai.PendingConfirm {
		t.Fatalf("got pending (ok=%v kind=%v), want PendingConfirm parked before the simulated restart", ok, before.Kind)
	}
	if got := countRowsWH(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 0 {
		t.Fatalf("got %d debts before confirmation, want 0", got)
	}

	// --- "the deploy": a brand-new Service and Processor, sharing only
	// the durable stores (pool, rdb) — nothing in-process carries over,
	// exactly what a real process restart does.
	svc2 := whatsapp.NewService(pool, rdb, "test-secret", "test-verify-token", "test-access-token", "test-phone-number-id", slog.Default())
	extractor2 := &scriptedExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentConfirmAction, Language: ai.LangEnglish},
	}}
	done2 := make(chan struct{}, 4)
	proc2 := ai.NewProcessor(ai.Config{
		Extractor: extractor2,
		Sender:    svc2,
		Media:     svc2,
		Pool:      pool,
		Redis:     rdb,
		Customers: customers,
		Debts:     debts,
		Payments:  payments,
		Logger:    slog.Default(),
	})
	svc2.SetHandler(func(ctx context.Context, msg whatsapp.StoredMessage) {
		uid := int64(0)
		if msg.UserID != nil {
			uid = *msg.UserID
		}
		_, _ = proc2.Handle(ctx, ai.ToInboundMessage(uid, msg.ProviderMessageID, msg.MessageType, msg.ContentReference))
		done2 <- struct{}{}
	})

	// --- Meta retries the exact same webhook delivery (same message id)
	// during the deploy window — must be caught as a duplicate and never
	// reach the handler at all, let alone be misread as a fresh reply to
	// the still-open confirmation.
	retried, err := svc2.ReceiveEvent(context.Background(), firstPayload)
	if err != nil {
		t.Fatalf("retried ReceiveEvent: %v", err)
	}
	if !retried[0].Duplicate {
		t.Fatalf("got %+v, want Duplicate=true — a redelivered webhook message id must be caught, restart or not", retried[0])
	}
	select {
	case <-done2:
		t.Fatal("handler was invoked for a duplicate delivery — a retried webhook must never reach the AI pipeline")
	case <-time.After(200 * time.Millisecond):
	}

	after, ok, err := ai.GetPendingAction(context.Background(), rdb, userID)
	if err != nil {
		t.Fatalf("GetPendingAction (after duplicate): %v", err)
	}
	if !ok || after.Kind != ai.PendingConfirm || after.Intent.CustomerName == nil || *after.Intent.CustomerName != "Chinedu" {
		t.Fatalf("got pending (ok=%v kind=%v intent=%+v), want the exact same PendingConfirm still parked, uncorrupted by the duplicate delivery", ok, after.Kind, after.Intent)
	}
	if got := countRowsWH(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 0 {
		t.Fatalf("got %d debts after the duplicate delivery, want still 0", got)
	}

	// --- the trader's real "yes," reaching the new process instance,
	// must still resolve the original pending confirmation correctly.
	confirmPayload := textPayload("16505559001", "wamid.restart.2", "yes")
	if _, err := svc2.ReceiveEvent(context.Background(), confirmPayload); err != nil {
		t.Fatalf("confirm ReceiveEvent: %v", err)
	}
	waitFor(t, done2, "confirm handler completion")

	if got := countRowsWH(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts after the real confirmation, want exactly 1 — the pending flow must have survived the restart intact", got)
	}
	if _, ok, err := ai.GetPendingAction(context.Background(), rdb, userID); err != nil || ok {
		t.Fatalf("got pending action still set (ok=%v err=%v) after confirmation, want cleared", ok, err)
	}
}

func waitFor(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func countRowsWH(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	return count
}
