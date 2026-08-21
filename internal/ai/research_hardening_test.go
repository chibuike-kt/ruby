package ai_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/customer"
	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/debt"
	"github.com/chibuike-kt/ruby/internal/money"
)

// onboardingMarkers are strings that only ever appear in the onboarding
// flow's own replies (firstContactReply/nameReaskText/nameAcceptedText)
// — never a legitimate substring of any other reply in the product.
var onboardingMarkers = []string{
	"what should I call you",
	"I just need your name",
	"I'm Ruby, your bookkeeping assistant",
	"Nice to meet you,",
}

func assertNeverOnboarding(t *testing.T, label, text string) {
	t.Helper()
	for _, marker := range onboardingMarkers {
		if strings.Contains(text, marker) {
			t.Fatalf("%s: got reply %q, contains onboarding marker %q — an already-named user must never be re-onboarded by a button tap", label, text, marker)
		}
	}
}

// TestProcessor_FlowBreakage_NoButtonEverResetsToOnboarding is docs/
// BRIEF-research-hardening-standard.md Part 3's "flow breakage" test
// class: "a process unexpectedly exiting or resetting due to a logic
// bug" is a named failure category (the exact class of bug chased
// earlier tonight, Edit resetting to onboarding, whether or not that
// specific instance was ever real). One test class driving every
// button from every flow that has one, asserting none of them ever
// produces an onboarding-flow response for an already-named user, is
// worth more than isolated regression tests per button.
func TestProcessor_FlowBreakage_NoButtonEverResetsToOnboarding(t *testing.T) {
	type flowCase struct {
		name     string
		buttonID string
		setup    func(t *testing.T) (*ai.Processor, int64)
	}

	confirmationSetup := func(wamid string) func(t *testing.T) (*ai.Processor, int64) {
		return func(t *testing.T) (*ai.Processor, int64) {
			pool := dbtest.Open(t)
			rdb := dbtest.OpenRedis(t)
			userID := dbtest.CreateUser(t, pool, "+23482200"+wamid)
			extractor := &fakeExtractor{results: []ai.RawIntent{
				{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
			}}
			p := newTestProcessor(pool, rdb, extractor, &fakePhraser{}, &fakeSender{})
			if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fb.setup."+wamid, "text", new("Chinedu took 75k"))); err != nil {
				t.Fatalf("setup (confirmation prompt): %v", err)
			}
			return p, userID
		}
	}

	identitySetup := func(wamid string) func(t *testing.T) (*ai.Processor, int64) {
		return func(t *testing.T) (*ai.Processor, int64) {
			pool := dbtest.Open(t)
			rdb := dbtest.OpenRedis(t)
			userID := dbtest.CreateUser(t, pool, "+23482200"+wamid)
			customers := customer.NewService(pool)
			if _, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil); err != nil {
				t.Fatalf("seed customer: %v", err)
			}
			extractor := &fakeExtractor{results: []ai.RawIntent{
				{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
			}}
			p := newTestProcessor(pool, rdb, extractor, &fakePhraser{}, &fakeSender{})
			if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fb.setup."+wamid, "text", new("Chinedu took 75k"))); err != nil {
				t.Fatalf("setup (identity confirmation prompt): %v", err)
			}
			return p, userID
		}
	}

	reminderOptInSetup := func(wamid string) func(t *testing.T) (*ai.Processor, int64) {
		return func(t *testing.T) (*ai.Processor, int64) {
			pool := dbtest.Open(t)
			rdb := dbtest.OpenRedis(t)
			userID := dbtest.CreateUser(t, pool, "+23482200"+wamid)
			dueDate := time.Now().Add(72 * time.Hour).Format(time.DateOnly)
			extractor := &fakeExtractor{results: []ai.RawIntent{
				{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), DueDateISO: &dueDate, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
			}}
			p := newTestProcessorWithReminders(pool, rdb, extractor, &fakePhraser{}, &fakeSender{}, &fakeTemplateSender{})
			if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fb.setup."+wamid, "text", new("Chinedu took 75k, due Friday"))); err != nil {
				t.Fatalf("setup (reminder opt-in prompt): %v", err)
			}
			return p, userID
		}
	}

	disambiguationSetup := func(wamid string) func(t *testing.T) (*ai.Processor, int64) {
		return func(t *testing.T) (*ai.Processor, int64) {
			pool := dbtest.Open(t)
			rdb := dbtest.OpenRedis(t)
			userID := dbtest.CreateUser(t, pool, "+23482200"+wamid)
			customers := customer.NewService(pool)
			if _, err := customers.Create(context.Background(), userID, "Chinedu", new("+2348030000001"), nil); err != nil {
				t.Fatalf("seed customer 1: %v", err)
			}
			if _, err := customers.Create(context.Background(), userID, "Chinedu", new("+2348030000002"), nil); err != nil {
				t.Fatalf("seed customer 2: %v", err)
			}
			extractor := &fakeExtractor{results: []ai.RawIntent{
				{Intent: ai.IntentRecordPayment, CustomerName: new("Chinedu"), AmountMinor: new(int64(500000)), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
			}}
			p := newTestProcessor(pool, rdb, extractor, &fakePhraser{}, &fakeSender{})
			if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fb.setup."+wamid, "text", new("Chinedu paid 5k"))); err != nil {
				t.Fatalf("setup (disambiguation prompt): %v", err)
			}
			return p, userID
		}
	}

	slotFillSetup := func(wamid string) func(t *testing.T) (*ai.Processor, int64) {
		return func(t *testing.T) (*ai.Processor, int64) {
			pool := dbtest.Open(t)
			rdb := dbtest.OpenRedis(t)
			userID := dbtest.CreateUser(t, pool, "+23482200"+wamid)
			extractor := &fakeExtractor{results: []ai.RawIntent{
				{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
			}}
			p := newTestProcessor(pool, rdb, extractor, &fakePhraser{}, &fakeSender{})
			if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fb.setup."+wamid, "text", new("someone took goods"))); err != nil {
				t.Fatalf("setup (slot-fill question): %v", err)
			}
			return p, userID
		}
	}

	greetingMenuSetup := func(wamid string) func(t *testing.T) (*ai.Processor, int64) {
		return func(t *testing.T) (*ai.Processor, int64) {
			pool := dbtest.Open(t)
			rdb := dbtest.OpenRedis(t)
			userID := dbtest.CreateUser(t, pool, "+23482200"+wamid)
			p := newTestProcessor(pool, rdb, &fakeExtractor{}, &fakePhraser{}, &fakeSender{})
			return p, userID
		}
	}

	cases := []flowCase{
		{"Confirm", "confirm", confirmationSetup("001")},
		{"Edit", "edit", confirmationSetup("002")},
		{"Cancel (confirmation)", "cancel", confirmationSetup("003")},
		{"Same person", "identity:same", identitySetup("004")},
		{"Someone new", "identity:new", identitySetup("005")},
		{"Yes (reminder opt-in)", "reminder:yes", reminderOptInSetup("006")},
		{"No (reminder opt-in)", "reminder:no", reminderOptInSetup("007")},
		{"Disambiguation candidate", "1", disambiguationSetup("008")},
		{"Cancel (slot-fill)", "cancel", slotFillSetup("009")},
		{"Greeting menu: Record a debt", "menu:create_debt", greetingMenuSetup("010")},
		{"Greeting menu: Who owes me?", "menu:balance", greetingMenuSetup("011")},
		{"Greeting menu: Help", "menu:help", greetingMenuSetup("012")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, userID := tc.setup(t)
			buttonID := tc.buttonID
			reply, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.fb.tap", "interactive", &buttonID))
			if err != nil {
				t.Fatalf("Handle (%s): %v", tc.name, err)
			}
			assertNeverOnboarding(t, tc.name, reply.Text)
		})
	}
}

// TestProcessor_SlotFill_ReaskNeverRepeatsSameSentenceTwiceInARow is
// docs/BRIEF-research-hardening-standard.md Part 3's phrasing-variation
// requirement: "repetitive identical fallback messages" is a named
// failure mode — three consecutive replies that don't answer the
// amount question must produce three re-asks with no two adjacent ones
// identical.
func TestProcessor_SlotFill_ReaskNeverRepeatsSameSentenceTwiceInARow(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348210000001")

	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish}, // amount missing
		{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},                               // reply 1: doesn't answer
		{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},                               // reply 2: doesn't answer
		{Intent: ai.IntentCreateDebt, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},                               // reply 3: doesn't answer
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	p := newTestProcessor(pool, rdb, extractor, phraser, sender)

	askAmount, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rh1.1", "text", new("Chinedu took goods")))
	if err != nil {
		t.Fatalf("1 (ask amount): %v", err)
	}

	reask1, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rh1.2", "text", new("not sure yet")))
	if err != nil {
		t.Fatalf("2 (reask 1): %v", err)
	}
	if reask1.Text == askAmount.Text {
		t.Fatalf("got reask 1 %q, same as the original question %q — must differ", reask1.Text, askAmount.Text)
	}

	reask2, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rh1.3", "text", new("still not sure")))
	if err != nil {
		t.Fatalf("3 (reask 2): %v", err)
	}
	if reask2.Text == reask1.Text {
		t.Fatalf("got reask 2 %q, identical to reask 1 %q — consecutive re-asks must never repeat the exact same sentence", reask2.Text, reask1.Text)
	}

	reask3, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rh1.4", "text", new("hmm")))
	if err != nil {
		t.Fatalf("4 (reask 3): %v", err)
	}
	if reask3.Text == reask2.Text {
		t.Fatalf("got reask 3 %q, identical to reask 2 %q — must alternate, not just vary once", reask3.Text, reask2.Text)
	}
	// Alternation means reask3 should match reask1 (back to the
	// original phrasing) — proving it's a stable two-way alternation,
	// not an ever-escalating chain that would eventually run out.
	if reask3.Text != reask1.Text {
		t.Fatalf("got reask 3 %q, want it to match reask 1 %q (alternating back)", reask3.Text, reask1.Text)
	}
}

// TestProcessor_ReminderPhone_ReaskNeverRepeatsSameSentenceTwiceInARow
// is the same requirement applied to the reminder phone-number flow.
func TestProcessor_ReminderPhone_ReaskNeverRepeatsSameSentenceTwiceInARow(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348210000002")

	dueDate := time.Now().Add(72 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateDebt, CustomerName: new("Chinedu"), AmountMinor: new(int64(7500000)), DueDateISO: &dueDate, Confidence: ai.ConfidenceHigh, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rh2.0", "text", new("Chinedu took 75k, due Friday"))); err != nil {
		t.Fatalf("0 (create debt): %v", err)
	}
	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rh2.1", "interactive", new("reminder:yes"))); err != nil {
		t.Fatalf("1 (opt in, ask phone): %v", err)
	}

	reask1, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rh2.2", "text", new("I'll find it")))
	if err != nil {
		t.Fatalf("2 (reask 1): %v", err)
	}

	reask2, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rh2.3", "text", new("one moment")))
	if err != nil {
		t.Fatalf("3 (reask 2): %v", err)
	}
	if reask2.Text == reask1.Text {
		t.Fatalf("got reask 2 %q, identical to reask 1 %q — must alternate phrasing", reask2.Text, reask1.Text)
	}
}

// TestProcessor_CreateReminder_LowConfidence_ConfirmThenSchedule is
// docs/BRIEF-research-hardening-standard.md Part 3's "extend confidence-
// based clarification everywhere an extraction is genuinely uncertain":
// CREATE_REMINDER schedules a real message to a real customer — the
// same stakes as CREATE_DEBT/RECORD_PAYMENT — so a low-confidence
// extraction must be confirmed first, not scheduled on a guess, mirroring
// TestProcessor_LowConfidence_ConfirmThenExecute's own pattern.
func TestProcessor_CreateReminder_LowConfidence_ConfirmThenSchedule(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	userID := dbtest.CreateUser(t, pool, "+2348190100099")
	customers := customer.NewService(pool)

	c, err := customers.Create(context.Background(), userID, "Chinedu", nil, nil)
	if err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	debtID := seedDebtNoDueDate(t, pool, userID, c.ID)

	tomorrowISO := time.Now().Add(24 * time.Hour).Format(time.DateOnly)
	extractor := &fakeExtractor{results: []ai.RawIntent{
		{Intent: ai.IntentCreateReminder, CustomerName: new("Chinedu"), DueDateISO: &tomorrowISO, Confidence: ai.ConfidenceLow, Language: ai.LangEnglish},
		{Intent: ai.IntentConfirmAction, Language: ai.LangEnglish},
	}}
	phraser := &fakePhraser{}
	sender := &fakeSender{}
	templateSender := &fakeTemplateSender{}
	p := newTestProcessorWithReminders(pool, rdb, extractor, phraser, sender, templateSender)

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rhconf.1", "text", new("remind him maybe tomorrow?"))); err != nil {
		t.Fatalf("Handle (first message): %v", err)
	}
	if phraser.last().Event != ai.EventReminderConfirmationNeeded {
		t.Fatalf("got phrase event %v, want EventReminderConfirmationNeeded — a low-confidence reminder must be confirmed before scheduling", phraser.last().Event)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM reminders WHERE debt_id = $1`, debtID); got != 0 {
		t.Fatalf("got %d reminders scheduled before confirmation, want 0", got)
	}

	if _, err := p.Handle(context.Background(), ai.ToInboundMessage(userID, "wamid.rhconf.2", "text", new("yes"))); err != nil {
		t.Fatalf("Handle (confirm): %v", err)
	}
	if got := countRows(t, pool, `SELECT count(*) FROM reminders WHERE debt_id = $1`, debtID); got == 0 {
		t.Fatalf("got %d reminders scheduled after confirmation, want at least 1", got)
	}
}

func seedDebtNoDueDate(t *testing.T, pool *pgxpool.Pool, userID, customerID int64) int64 {
	t.Helper()
	debts := debt.NewService(pool)
	d, err := debts.Create(context.Background(), userID, customerID, money.New(7500000, money.NGN), "rice", nil)
	if err != nil {
		t.Fatalf("seed debt: %v", err)
	}
	return d.ID
}
