package ai_test

import (
	"context"
	"testing"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/payment"
)

// fakeVision is docs/BRIEF-research-hardening-standard.md Part 5 Tier
// 1's photo-input double: a photo is only ever looked at once per
// message (queue continuation replays the already-extracted RawIntents,
// never calls Vision again), so one scripted transaction list per test
// is enough.
type fakeVision struct {
	txns  []ai.RawIntent
	calls int
}

func (f *fakeVision) ExtractFromImage(_ context.Context, _ []byte, _ string) ([]ai.RawIntent, error) {
	f.calls++
	return f.txns, nil
}

func TestProcessor_Image_SingleCleanTransaction_ExecutesImmediately(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348210000001")

	vision := &fakeVision{txns: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	sender := &fakeSender{}
	p := ai.NewProcessor(ai.Config{
		Extractor: &fakeExtractor{}, // must never be called for image input
		Phraser:   &fakePhraser{},
		Sender:    sender,
		Media:     fakeMedia{data: []byte("photo-bytes")},
		Vision:    vision,
		Pool:      pool,
		Redis:     rdb,
		Customers: customer.NewService(pool),
		Debts:     debt.NewService(pool),
		Payments:  payment.NewService(pool),
	})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.1", "image", new("media-id-1")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) != 0 {
		t.Fatalf("got %d buttons, want a clean high-confidence transaction to execute with no confirmation needed", len(reply.Buttons))
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts, want 1", got)
	}
	if vision.calls != 1 {
		t.Fatalf("got %d vision calls, want exactly 1", vision.calls)
	}
	if _, ok, _ := ai.GetPendingAction(context.Background(), rdb, userID); ok {
		t.Fatal("got a pending action left over after a single clean transaction, want none")
	}
}

func TestProcessor_Image_MultipleCleanTransactions_AllExecute(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348210000002")

	vision := &fakeVision{txns: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, CustomerName: new("Ngozi"), AmountMinor: new(int64(3000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, CustomerName: new("Uche"), AmountMinor: new(int64(1000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	p := ai.NewProcessor(ai.Config{
		Extractor: &fakeExtractor{},
		Phraser:   &fakePhraser{},
		Sender:    &fakeSender{},
		Media:     fakeMedia{data: []byte("photo-bytes")},
		Vision:    vision,
		Pool:      pool,
		Redis:     rdb,
		Customers: customer.NewService(pool),
		Debts:     debt.NewService(pool),
		Payments:  payment.NewService(pool),
	})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.2", "image", new("media-id-1")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 3 {
		t.Fatalf("got %d debts, want 3 — every clean transaction from the photo must execute, not just the first", got)
	}
	if reply.Text == "" {
		t.Fatal("got an empty combined reply for 3 executed transactions")
	}
	if _, ok, _ := ai.GetPendingAction(context.Background(), rdb, userID); ok {
		t.Fatal("got a pending action left over after every transaction executed cleanly, want none")
	}
}

// TestProcessor_Image_LowConfidenceMidQueue_ConfirmContinuesRest is the
// brief's core requirement: a low-confidence transaction from a photo
// gets the exact same single-transaction Confirm/Edit/Cancel flow real
// text messages use — never a separate bulk-confirm UI — and confirming
// it resumes the rest of the photo's transactions automatically.
func TestProcessor_Image_LowConfidenceMidQueue_ConfirmContinuesRest(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348210000003")

	vision := &fakeVision{txns: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(5000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, CustomerName: new("Ngozi"), AmountMinor: new(int64(3000000)), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, CustomerName: new("Uche"), AmountMinor: new(int64(1000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	p := ai.NewProcessor(ai.Config{
		Extractor: &fakeExtractor{},
		Phraser:   &fakePhraser{},
		Sender:    &fakeSender{},
		Media:     fakeMedia{data: []byte("photo-bytes")},
		Vision:    vision,
		Pool:      pool,
		Redis:     rdb,
		Customers: customer.NewService(pool),
		Debts:     debt.NewService(pool),
		Payments:  payment.NewService(pool),
	})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.3", "image", new("media-id-1")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) != 3 {
		t.Fatalf("got %d buttons, want the real single-transaction Confirm/Edit/Cancel prompt (3 buttons) for Ngozi's low-confidence entry", len(reply.Buttons))
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts before confirming, want 1 (only Chinedu's, executed before the queue paused)", got)
	}
	pending, ok, err := ai.GetPendingAction(context.Background(), rdb, userID)
	if err != nil || !ok || pending.Kind != ai.PendingConfirm {
		t.Fatalf("got pending (ok=%v kind=%v err=%v), want PendingConfirm parked for Ngozi", ok, pending.Kind, err)
	}
	if len(pending.Queue) != 1 {
		t.Fatalf("got queue length %d, want 1 (Uche still waiting)", len(pending.Queue))
	}

	confirmID := "confirm"
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.3b", "interactive", &confirmID)); err != nil {
		t.Fatalf("Handle (confirm): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 3 {
		t.Fatalf("got %d debts after confirming, want 3 — Uche's transaction must have resumed automatically", got)
	}
	if _, ok, _ := ai.GetPendingAction(context.Background(), rdb, userID); ok {
		t.Fatal("got a pending action left over after the whole photo queue resolved, want none")
	}
}

// TestProcessor_Image_CancelMidQueue_ContinuesRest confirms declining
// one transaction from a photo never discards the others.
func TestProcessor_Image_CancelMidQueue_ContinuesRest(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348210000004")

	vision := &fakeVision{txns: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Ngozi"), AmountMinor: new(int64(3000000)), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, CustomerName: new("Uche"), AmountMinor: new(int64(1000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	p := ai.NewProcessor(ai.Config{
		Extractor: &fakeExtractor{},
		Phraser:   &fakePhraser{},
		Sender:    &fakeSender{},
		Media:     fakeMedia{data: []byte("photo-bytes")},
		Vision:    vision,
		Pool:      pool,
		Redis:     rdb,
		Customers: customer.NewService(pool),
		Debts:     debt.NewService(pool),
		Payments:  payment.NewService(pool),
	})

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.4", "image", new("media-id-1"))); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	cancelID := "cancel"
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.4b", "interactive", &cancelID)); err != nil {
		t.Fatalf("Handle (cancel): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts after cancelling Ngozi's, want 1 — Uche's must still have gone through", got)
	}
	var name string
	if err := pool.QueryRow(context.Background(),
		`SELECT c.name FROM debts d JOIN customers c ON c.id = d.customer_id WHERE d.user_id = $1`, userID,
	).Scan(&name); err != nil {
		t.Fatalf("query debt customer: %v", err)
	}
	if name != "Uche" {
		t.Fatalf("got debt for %q, want Uche (Ngozi's was cancelled, not Uche's)", name)
	}
}

// TestProcessor_Image_MissingFieldMidQueue_SlotFillThenContinues covers
// a transaction the photo left genuinely incomplete (no legible amount)
// — slot-fill asks, exactly like it would for a text message, and
// answering it resumes the rest of the queue.
func TestProcessor_Image_MissingFieldMidQueue_SlotFillThenContinues(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348210000005")

	vision := &fakeVision{txns: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Ngozi"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // amount not legible
		{Intent: ai.IntentCreateDebt, CustomerName: new("Uche"), AmountMinor: new(int64(1000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, AmountMinor: new(int64(3000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	p := ai.NewProcessor(ai.Config{
		Extractor: extractor,
		Phraser:   &fakePhraser{},
		Sender:    &fakeSender{},
		Media:     fakeMedia{data: []byte("photo-bytes")},
		Vision:    vision,
		Pool:      pool,
		Redis:     rdb,
		Customers: customer.NewService(pool),
		Debts:     debt.NewService(pool),
		Payments:  payment.NewService(pool),
	})

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.5", "image", new("media-id-1"))); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	pending, ok, err := ai.GetPendingAction(context.Background(), rdb, userID)
	if err != nil || !ok || pending.Kind != ai.PendingSlotFill {
		t.Fatalf("got pending (ok=%v kind=%v err=%v), want PendingSlotFill for Ngozi's missing amount", ok, pending.Kind, err)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.5b", "text", new("3k"))); err != nil {
		t.Fatalf("Handle (fill amount): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 2 {
		t.Fatalf("got %d debts, want 2 — Ngozi's (once completed) and Uche's (resumed after)", got)
	}
}

func TestProcessor_Image_NoTransactionsFound_FriendlyDecline(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348210000006")

	vision := &fakeVision{txns: nil}
	p := ai.NewProcessor(ai.Config{
		Extractor: &fakeExtractor{},
		Phraser:   &fakePhraser{},
		Sender:    &fakeSender{},
		Media:     fakeMedia{data: []byte("photo-bytes")},
		Vision:    vision,
		Pool:      pool,
		Redis:     rdb,
		Customers: customer.NewService(pool),
		Debts:     debt.NewService(pool),
		Payments:  payment.NewService(pool),
	})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.6", "image", new("media-id-1")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if reply.Text == "" {
		t.Fatal("got an empty reply when no transactions were found in the photo, want a friendly decline")
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 0 {
		t.Fatalf("got %d debts, want 0", got)
	}
}

// TestProcessor_Image_IdentityConfirmationMidQueue_QueueDroppedWithNotice
// covers the deliberate, narrower scope: identity confirmation doesn't
// resume a photo queue automatically (unlike confirmation/slot-fill/
// reminder opt-in) — the trader is told honestly, never left silently
// stuck wondering where the rest of the photo went.
func TestProcessor_Image_IdentityConfirmationMidQueue_QueueDroppedWithNotice(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348210000007")
	customers := customer.NewService(pool)
	if _, err := customers.Create(context.Background(), userID, "Ngozi", nil, nil); err != nil {
		t.Fatalf("seed customer: %v", err)
	}

	vision := &fakeVision{txns: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Ngozi"), AmountMinor: new(int64(3000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, CustomerName: new("Uche"), AmountMinor: new(int64(1000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	p := ai.NewProcessor(ai.Config{
		Extractor: &fakeExtractor{},
		Phraser:   &fakePhraser{},
		Sender:    &fakeSender{},
		Media:     fakeMedia{data: []byte("photo-bytes")},
		Vision:    vision,
		Pool:      pool,
		Redis:     rdb,
		Customers: customers,
		Debts:     debt.NewService(pool),
		Payments:  payment.NewService(pool),
	})

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.7", "image", new("media-id-1")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(reply.Buttons) != 2 {
		t.Fatalf("got %d buttons, want the same/new identity prompt for Ngozi", len(reply.Buttons))
	}
	pending, ok, err := ai.GetPendingAction(context.Background(), rdb, userID)
	if err != nil || !ok || pending.Kind != ai.PendingIdentityConfirm {
		t.Fatalf("got pending (ok=%v kind=%v err=%v), want PendingIdentityConfirm", ok, pending.Kind, err)
	}
	if len(pending.Queue) != 0 {
		t.Fatalf("got queue length %d, want 0 — identity confirmation deliberately doesn't carry the rest of the photo queue forward", len(pending.Queue))
	}

	sameID := "identity:same"
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.photo.7b", "interactive", &sameID)); err != nil {
		t.Fatalf("Handle (same): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts, want 1 (only Ngozi's) — Uche's was deliberately not auto-resumed", got)
	}
}
