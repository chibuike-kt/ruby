package ai_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/money"
	"github.com/chibuike-kt/ruby/internal/payment"
)

// fakeExtractor returns a scripted RawIntent per call, in order — the
// last one repeats if Extract is called more times than scripted.
type fakeExtractor struct {
	mu      sync.Mutex
	results []ai.RawIntent
	calls   []string
}

func (f *fakeExtractor) Extract(_ context.Context, text string) (ai.RawIntent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, text)
	if len(f.results) == 0 {
		return ai.RawIntent{}, errors.New("fakeExtractor: no more scripted results")
	}
	r := f.results[0]
	if len(f.results) > 1 {
		f.results = f.results[1:]
	}
	return r, nil
}

// fakePhraser records every PhraseInput it was given — the boundary
// test relies on this being the *only* thing it ever sees.
type fakePhraser struct {
	mu     sync.Mutex
	inputs []ai.PhraseInput
}

func (f *fakePhraser) Phrase(_ context.Context, input ai.PhraseInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, input)
	return fmt.Sprintf("[%s/%s]", input.Event, input.Language), nil
}

func (f *fakePhraser) last() ai.PhraseInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.inputs) == 0 {
		return ai.PhraseInput{}
	}
	return f.inputs[len(f.inputs)-1]
}

type fakeSender struct {
	mu   sync.Mutex
	sent []string
}

func (f *fakeSender) SendText(_ context.Context, _, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, body)
	return nil
}

// fakeTranscoder prefixes its input so tests can prove the transcoded
// (not raw) bytes are what reaches the transcriber.
type fakeTranscoder struct{}

func (fakeTranscoder) Transcode(_ context.Context, ogg []byte) ([]byte, error) {
	return append([]byte("transcoded:"), ogg...), nil
}

type fakeTranscriber struct {
	text        string
	lang        ai.Language
	receivedRef *[]byte
}

func (f fakeTranscriber) Transcribe(_ context.Context, audio []byte, _ []ai.Language) (string, ai.Language, error) {
	if f.receivedRef != nil {
		*f.receivedRef = audio
	}
	return f.text, f.lang, nil
}

type fakeMedia struct {
	data []byte
}

func (f fakeMedia) DownloadMedia(_ context.Context, _ string) ([]byte, string, error) {
	return f.data, "audio/ogg", nil
}

// fakeHallucinatingPhraser always returns a fixed reply that states a
// number that never appears in whatever PhraseInput it's given —
// standing in for a model that ignores its input and invents a figure,
// exactly the failure mode isGrounded exists to catch.
type fakeHallucinatingPhraser struct {
	reply string
}

func (f fakeHallucinatingPhraser) Phrase(_ context.Context, _ ai.PhraseInput) (string, error) {
	return f.reply, nil
}

func newTestProcessor(pool *pgxpool.Pool, rdb *redis.Client, extractor ai.Extractor, phraser ai.Phraser, sender ai.Sender) *ai.Processor {
	return ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{},
		Transcoder:  fakeTranscoder{},
		Phraser:     phraser,
		Sender:      sender,
		Media:       fakeMedia{},
		Pool:        pool,
		Redis:       rdb,
		Customers:   customer.NewService(pool),
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
	})
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return count
}

func TestProcessor_LowConfidence_ConfirmThenExecute(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
		{Intent: ai.IntentConfirmAction, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	msg1 := ai.ToInboundMessage(userID, "wamid.lowconf.1", "text", new("Chinedu took 75k, pays Friday"))
	if _, err := p.Handle(context.Background(), msg1); err != nil {
		t.Fatalf("Handle (first message): %v", err)
	}
	if phraser.last().Event != ai.EventConfirmationNeeded {
		t.Fatalf("got phrase event %v, want EventConfirmationNeeded", phraser.last().Event)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 0 {
		t.Fatalf("got %d debts before confirmation, want 0", got)
	}

	msg2 := ai.ToInboundMessage(userID, "wamid.lowconf.2", "text", new("yes"))
	if _, err := p.Handle(context.Background(), msg2); err != nil {
		t.Fatalf("Handle (confirm): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts after confirmation, want 1", got)
	}
	if phraser.last().Event != ai.EventDebtCreated {
		t.Fatalf("got phrase event %v after confirmation, want EventDebtCreated", phraser.last().Event)
	}
}

// TestProcessor_DebtCreated_OutstandingEqualsFullAmount is the
// regression test for the reported bug: a freshly created debt has no
// payments against it, so the outcome object handed to the phrasing
// call must always report outstanding_minor equal to the full debt
// amount — never zero, and never left unset (a prior bug did the
// latter, which a Go zero-value int64 field then silently rendered as a
// real ₦0 to the model).
func TestProcessor_DebtCreated_OutstandingEqualsFullAmount(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000013")

	const wantAmountMinor = int64(7500000)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(wantAmountMinor), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	msg := ai.ToInboundMessage(userID, "wamid.debtcreated.1", "text", new("Chinedu took 75k"))
	if _, err := p.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	input := phraser.last()
	if input.Event != ai.EventDebtCreated {
		t.Fatalf("got phrase event %v, want EventDebtCreated", input.Event)
	}
	if input.AmountMinor == nil || *input.AmountMinor != wantAmountMinor {
		t.Fatalf("got amount_minor %v, want %d", input.AmountMinor, wantAmountMinor)
	}
	if input.OutstandingMinor == nil {
		t.Fatal("got a nil outstanding_minor for a freshly created debt, want it set to the full amount")
	}
	if *input.OutstandingMinor != wantAmountMinor {
		t.Fatalf("got outstanding_minor %d, want %d (the full amount — nothing's been paid yet)", *input.OutstandingMinor, wantAmountMinor)
	}
}

// TestProcessor_TwoNearIdenticalDebtMessages_SameOutstandingEachTime is
// the second half of the bug report: two near-identical debt-creation
// messages must produce a consistently-populated outcome object each
// time — not "sometimes with the outstanding line, sometimes without."
// That inconsistency traced back to the same root cause (the field
// being left unset), so proving it's always set for repeated,
// independent debts closes it.
func TestProcessor_TwoNearIdenticalDebtMessages_SameOutstandingEachTime(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000014")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentCreateDebt, CustomerName: new("Ngozi"), AmountMinor: new(int64(1200000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.near1.1", "text", new("Chinedu took 75k"))); err != nil {
		t.Fatalf("Handle (first debt): %v", err)
	}
	first := phraser.last()

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.near1.2", "text", new("Ngozi took 12k"))); err != nil {
		t.Fatalf("Handle (second debt): %v", err)
	}
	second := phraser.last()

	for name, input := range map[string]ai.PhraseInput{"first": first, "second": second} {
		if input.OutstandingMinor == nil {
			t.Fatalf("%s message: got a nil outstanding_minor, want it set — every DEBT_CREATED outcome must be consistent", name)
		}
		if input.AmountMinor == nil || *input.OutstandingMinor != *input.AmountMinor {
			t.Fatalf("%s message: got outstanding_minor %v vs amount_minor %v, want them equal for a brand-new debt", name, input.OutstandingMinor, input.AmountMinor)
		}
	}
}

// TestProcessor_HallucinatedPhrasing_FallsBackSafely proves the
// structural half of the fix end to end: even when the Phraser itself
// states a number that was never in its input (simulating a model that
// ignores its instructions), Processor.phrase's isGrounded check catches
// it and the trader never sees the hallucinated text — they get a safe,
// success-confirming fallback instead (never one implying the debt
// wasn't recorded, since it was).
func TestProcessor_HallucinatedPhrasing_FallsBackSafely(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000015")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	const hallucinated = "Debt recorded! Chinedu's outstanding balance: NGN 0."
	phraser := fakeHallucinatingPhraser{reply: hallucinated}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.hallucinate.1", "text", new("Chinedu took 75k")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if reply == hallucinated {
		t.Fatal("got the hallucinated reply verbatim — isGrounded should have rejected it")
	}
	if strings.Contains(reply, "NGN 0") || strings.Contains(reply, "₦0") {
		t.Fatalf("got a fallback reply that still states an incorrect ₦0, want no number at all: %q", reply)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts, want 1 — the mutation must still have succeeded even though phrasing was rejected", got)
	}
	if len(sender.sent) != 1 || sender.sent[0] != reply {
		t.Fatalf("got sent messages %v, want exactly the safe fallback reply to have been sent", sender.sent)
	}
}

func TestProcessor_LowConfidence_NonConfirmDropsPending(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000010")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
		{Intent: ai.IntentHelp, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.moveon.1", "text", new("Chinedu took 75k"))); err != nil {
		t.Fatalf("Handle (first message): %v", err)
	}

	// A reply that isn't CONFIRM_ACTION means the trader moved on (plan
	// decision #3) — must not execute the stale pending debt creation.
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.moveon.2", "text", new("never mind, help"))); err != nil {
		t.Fatalf("Handle (moved on): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 0 {
		t.Fatalf("got %d debts after the trader moved on instead of confirming, want 0", got)
	}
}

func TestProcessor_AmbiguousCustomer_NumericDisambiguationThenExecute(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000002")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	c1, err := customers.Create(context.Background(), userID, "Chinedu", new("+2348030000011"), nil)
	if err != nil {
		t.Fatalf("seed customer 1: %v", err)
	}
	c2, err := customers.Create(context.Background(), userID, "Chinedu", new("+2348030000012"), nil)
	if err != nil {
		t.Fatalf("seed customer 2: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c1.ID, money.New(1000000, money.NGN), "2 cartons of noodles", nil); err != nil {
		t.Fatalf("seed debt 1: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c2.ID, money.New(2000000, money.NGN), "1 bag of rice", nil); err != nil {
		t.Fatalf("seed debt 2: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentRecordPayment, CustomerName: new("Chinedu"), AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.ambig.1", "text", new("Chinedu paid me 5k"))); err != nil {
		t.Fatalf("Handle (first message): %v", err)
	}
	if phraser.last().Event != ai.EventAmbiguousCustomer {
		t.Fatalf("got phrase event %v, want EventAmbiguousCustomer", phraser.last().Event)
	}

	// The disambiguation reply is matched locally — fakeExtractor has no
	// more scripted results, so a second extraction call here would
	// error and fail this test, proving no AI call was made for it.
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.ambig.2", "text", new("2"))); err != nil {
		t.Fatalf("Handle (disambiguation reply): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 1 {
		t.Fatalf("got %d payments after disambiguation, want 1", got)
	}
}

func TestProcessor_Overpayment_ConfirmsAtOutstandingNotAttempted(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000003")
	customers := customer.NewService(pool)
	debts := debt.NewService(pool)

	c, err := customers.Create(context.Background(), userID, "Ngozi", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if _, err := debts.Create(context.Background(), userID, c.ID, money.New(7500000, money.NGN), "rice", nil); err != nil {
		t.Fatalf("seed debt: %v", err)
	}

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentRecordPayment, CustomerName: new("Ngozi"), AmountMinor: new(int64(10000000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
		{Intent: ai.IntentConfirmAction, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.overpay.1", "text", new("Ngozi paid 100k"))); err != nil {
		t.Fatalf("Handle (overpayment attempt): %v", err)
	}
	if phraser.last().Event != ai.EventOverpaymentPrompt {
		t.Fatalf("got phrase event %v, want EventOverpaymentPrompt", phraser.last().Event)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM payments`); got != 0 {
		t.Fatalf("got %d payments before confirmation, want 0", got)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.overpay.2", "text", new("yes"))); err != nil {
		t.Fatalf("Handle (confirm overpayment): %v", err)
	}

	var amountMinor int64
	err = pool.QueryRow(context.Background(),
		`SELECT amount_minor FROM payments WHERE debt_id = (SELECT id FROM debts WHERE customer_id = $1)`, c.ID,
	).Scan(&amountMinor)
	if err != nil {
		t.Fatalf("query payment: %v", err)
	}
	if amountMinor != 7500000 {
		t.Fatalf("got payment amount %d, want 7500000 (outstanding, not the 10000000 the trader said — decisions.md #6)", amountMinor)
	}
}

func TestProcessor_Multilingual(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)

	cases := []struct {
		name string
		lang ai.Language
		text string
	}{
		{"english", ai.LangEnglish, "who owes me?"},
		{"pidgin", ai.LangPidgin, "who dey owe me?"},
		{"yoruba", ai.LangYoruba, "ta ni o je mi ni gbese?"},
		{"igbo", ai.LangIgbo, "onye ji m ugwo?"},
		{"hausa", ai.LangHausa, "wa ke bin ni bashi?"},
		{"codeswitched", ai.LangPidgin, "Chinedu carry two carton noodles, e go pay Friday"},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userID := dbtest.CreateUser(t, pool, fmt.Sprintf("+234806000%04d", i))
			extractor := &fakeExtractor{results: []ai.RawIntent{
				{Intent: ai.IntentListOutstandingDebts, Language: tc.lang},
			}}
			phraser := &fakePhraser{}
			sender := &fakeSender{}
			p := newTestProcessor(pool, rdb, extractor, phraser, sender)

			text := tc.text
			msg := ai.ToInboundMessage(userID, fmt.Sprintf("wamid.lang.%d", i), "text", &text)
			if _, err := p.Handle(context.Background(), msg); err != nil {
				t.Fatalf("Handle: %v", err)
			}

			last := phraser.last()
			if last.Language != tc.lang {
				t.Fatalf("got phrase language %q, want %q", last.Language, tc.lang)
			}
			if last.Event != ai.EventOutstandingList {
				t.Fatalf("got phrase event %v, want EventOutstandingList", last.Event)
			}
		})
	}
}

func TestProcessor_VoicePipeline(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000009")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	var receivedByTranscriber []byte

	p := ai.NewProcessor(ai.Config{
		Extractor:   extractor,
		Transcriber: fakeTranscriber{text: "Chinedu took 75k", lang: ai.LangEnglish, receivedRef: &receivedByTranscriber},
		Transcoder:  fakeTranscoder{},
		Phraser:     phraser,
		Sender:      sender,
		Media:       fakeMedia{data: []byte("raw-ogg-bytes")},
		Pool:        pool,
		Redis:       rdb,
		Customers:   customer.NewService(pool),
		Debts:       debt.NewService(pool),
		Payments:    payment.NewService(pool),
	})

	msg := ai.ToInboundMessage(userID, "wamid.voice.1", "audio", new("media-id-1"))
	if _, err := p.Handle(context.Background(), msg); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got := countRows(t, pool, `SELECT count(*) FROM debts WHERE user_id = $1`, userID); got != 1 {
		t.Fatalf("got %d debts after the voice pipeline, want 1", got)
	}
	if string(receivedByTranscriber) != "transcoded:raw-ogg-bytes" {
		t.Fatalf("got transcriber input %q, want the transcoded (not raw) bytes — download -> transcode -> transcribe order matters", receivedByTranscriber)
	}
	if len(extractor.calls) != 1 || extractor.calls[0] != "Chinedu took 75k" {
		t.Fatalf("got extractor calls %v, want a single call with the transcribed text", extractor.calls)
	}
}

// TestPhraseInput_NeverCarriesRawText is the structural half of plan
// decision #7: no field on PhraseInput could plausibly hold a raw
// trader message, so the boundary can't quietly erode later by someone
// threading the original text through "just this once."
func TestPhraseInput_NeverCarriesRawText(t *testing.T) {
	forbidden := []string{"text", "message", "body", "raw"}
	for field := range reflect.TypeFor[ai.PhraseInput]().Fields() {
		lower := strings.ToLower(field.Name)
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Fatalf("PhraseInput has field %q, which looks like it could carry raw trader text — plan decision #7 relies on this never being true", field.Name)
			}
		}
	}
}

func TestProcessor_UnsupportedMessageType(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000011")

	extractor := &fakeExtractor{} // no scripted results: must not be called
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.image.1", "image", nil))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if reply == "" {
		t.Fatal("got an empty reply for an unsupported message type, want a friendly fallback")
	}
	if len(extractor.calls) != 0 {
		t.Fatalf("got %d extractor calls for an unsupported message type, want 0", len(extractor.calls))
	}
}

func TestProcessor_ConfirmAction_NoPending(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348050000012")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentConfirmAction, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.noconfirm.1", "text", new("yes")))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if reply == "" {
		t.Fatal("got an empty reply for a stray confirmation, want a friendly fallback")
	}
	if len(phraser.inputs) != 0 {
		t.Fatalf("got %d Phraser calls for a fixed-string response, want 0", len(phraser.inputs))
	}
}
