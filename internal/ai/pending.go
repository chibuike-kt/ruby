package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/customer"
)

type PendingKind string

const (
	PendingConfirm              PendingKind = "confirm"
	PendingDisambiguateCustomer PendingKind = "disambiguate_customer"
	PendingAwaitingName         PendingKind = "awaiting_name"

	// PendingIdentityConfirm and PendingAwaitingCustomerSignal implement
	// decisions.md #9 (docs/BRIEF-fixes-and-reminders.md #3): a name
	// match to exactly one existing customer, with nothing else
	// confirming it's them, asks same-or-new rather than guessing.
	// PendingIdentityConfirm is that question itself; a "new" answer
	// moves to PendingAwaitingCustomerSignal, decisions.md #8's
	// creation-time guard (a phone number or alias, so the new record
	// isn't an indistinguishable duplicate of the one just ruled out).
	PendingIdentityConfirm        PendingKind = "identity_confirm"
	PendingAwaitingCustomerSignal PendingKind = "awaiting_customer_signal"

	// PendingReminderOptIn and PendingAwaitingReminderPhone implement
	// docs/BRIEF-fixes-and-reminders.md #4: the yes/no question right
	// after a debt is created, and — only on "yes" with no phone number
	// already on file — the follow-up asking for one.
	PendingReminderOptIn         PendingKind = "reminder_opt_in"
	PendingAwaitingReminderPhone PendingKind = "awaiting_reminder_phone"

	// PendingSlotFill is the interactive slot-filling section's own
	// state (docs/BRIEF-critical-fixes-and-reminders.md): CREATE_DEBT
	// or RECORD_PAYMENT is missing a required field (customer identity
	// or amount — see slotfill.go). Intent carries the partial intent
	// being built up across replies; no new fields needed on
	// PendingAction beyond what already exists.
	PendingSlotFill PendingKind = "slot_fill"
)

// PendingCandidateOption is one candidate in a disambiguation prompt —
// enough to match a trader's reply locally (see disambiguate.go)
// without a second AI call, and enough to rebuild the same
// buttons/list on a re-ask (Name, for the button/row title).
type PendingCandidateOption struct {
	CustomerID int64
	Name       string
	Phone      string
	Hint       string
	// HintKind labels what Hint actually is (alias, phone, last item
	// description, or creation date) — docs/BRIEF-research-hardening-
	// standard.md Part 5 live-testing finding #2: an unlabeled hint
	// string lets an alias ("Atlas") and an item description ("two
	// cartons of noodles") render identically, misreading a person's
	// nickname as something they bought.
	HintKind customer.HintKind
}

// PendingOriginalMessage preserves a trader's original message verbatim
// while their name is being captured
// (docs/BRIEF-response-quality.md #1) — replayed through
// Processor.process once a name is known (see acceptName), so a trader
// whose first message was a real request never has to repeat
// themselves. Mirrors InboundMessage's shape rather than embedding it
// directly, so this package's Redis-serialized state doesn't couple to
// InboundMessage's field set changing shape later.
type PendingOriginalMessage struct {
	ProviderMessageID string
	Type              string // "text", "audio", or "interactive"
	Text              *string
	MediaID           *string
	InteractiveID     *string
}

// originalMessageFrom captures msg for later replay.
func originalMessageFrom(msg InboundMessage) *PendingOriginalMessage {
	return &PendingOriginalMessage{
		ProviderMessageID: msg.ProviderMessageID,
		Type:              msg.Type,
		Text:              msg.Text,
		MediaID:           msg.MediaID,
		InteractiveID:     msg.InteractiveID,
	}
}

// toInboundMessage reconstructs the original message for the given
// user so it can be run back through Processor.process.
func (o *PendingOriginalMessage) toInboundMessage(userID int64) InboundMessage {
	return InboundMessage{
		UserID:            userID,
		ProviderMessageID: o.ProviderMessageID,
		Type:              o.Type,
		Text:              o.Text,
		MediaID:           o.MediaID,
		InteractiveID:     o.InteractiveID,
	}
}

// isBareGreeting reports whether the original message was nothing but a
// greeting — if so, acceptName doesn't replay it (plan step 6: "if the
// original first message had real content beyond a greeting"). A nil
// original (nothing was ever captured) counts as bare — there's nothing
// to replay either way. Audio/interactive originals are always treated
// as real content: transcribing just to check would defeat the point of
// this being a cheap, deterministic check, and a button tap is
// explicitly "a clear, valid intent" per plan step 4, never a bare
// greeting.
func (o *PendingOriginalMessage) isBareGreeting() bool {
	if o == nil {
		return true
	}
	if o.Type != "text" || o.Text == nil {
		return false
	}
	_, ok := greetingLanguage(*o.Text)
	return ok
}

// PendingAction is parked in Redis awaiting one more piece of trader
// input before Processor executes it — a confirmation, a disambiguation
// reply, or (per docs/BRIEF-response-quality.md #1) a name. Kind, not a
// separate store per case, since all three are "the same intent, needs
// one more answer."
type PendingAction struct {
	Kind       PendingKind
	Intent     RawIntent
	Candidates []PendingCandidateOption

	// OriginalMessage is used by PendingAwaitingName only. ReaskCount
	// started there too, and is now reused by PendingSlotFill and
	// PendingAwaitingReminderPhone (docs/BRIEF-research-hardening-
	// standard.md Part 3: "repetitive identical fallback messages" is a
	// named failure mode) — how many times the *current* question has
	// been re-asked, so consecutive re-asks can alternate phrasing
	// instead of repeating the exact same sentence. Reset to 0 whenever
	// a fresh PendingAction is written for a different question (moving
	// to the next slot-fill field, a brand new pending flow, etc.) —
	// every SetPendingAction call that isn't explicitly incrementing it
	// leaves it at Go's zero value, which is the reset.
	OriginalMessage *PendingOriginalMessage
	ReaskCount      int

	// DebtID is used by PendingReminderOptIn and
	// PendingAwaitingReminderPhone only (docs/BRIEF-fixes-and-
	// reminders.md #4) — the debt the reminder opt-in question is
	// about. The customer and due date are re-fetched from it rather
	// than duplicated here, so this pending state can never go stale
	// relative to the debt record itself.
	DebtID *int64

	// Queue is the not-yet-processed remainder of a multi-transaction
	// photo (docs/BRIEF-research-hardening-standard.md Part 5 Tier 1:
	// photo input properly resolves the earlier-declined multi-
	// transaction question). A photo showing several transactions never
	// gets a separate bulk-confirm flow — each one rides through this
	// same PendingAction machinery, one at a time: whichever transaction
	// is currently pending (whatever Kind it needed — confirmation or a
	// missing slot) carries the rest of the photo's transactions here,
	// and continueQueue resumes them once this one resolves. Empty for
	// every ordinary (non-photo) pending state.
	Queue []RawIntent
}

// DefaultPendingTTL matches the conversational-context window the rest
// of Ruby already uses — after this long without a reply, Ruby should
// ask fresh rather than resume a stale exchange.
const DefaultPendingTTL = customer.DefaultLastCustomerContextTTL

// ReminderOptInPendingTTL is deliberately much longer than
// DefaultPendingTTL — docs/BRIEF-research-hardening-standard.md Part 5
// live-testing finding #4: a "Yes, remind them" button sent right after
// a debt-created confirmation isn't part of an active back-and-forth
// the trader might have abandoned (unlike a slot-fill question or a
// same/new prompt); it's a standalone yes/no offer that legitimately
// sits unanswered on a trader's phone for a while. A tap 21 minutes
// later — past DefaultPendingTTL's 10-minute window — was falling
// through to the generic help fallback instead of a real answer.
const ReminderOptInPendingTTL = 24 * time.Hour

func pendingKey(userID int64) string {
	return fmt.Sprintf("ruby:ctx:%d:pending", userID)
}

// SetPendingAction records p as the thing Ruby is waiting on from userID.
func SetPendingAction(ctx context.Context, rdb *redis.Client, userID int64, p PendingAction, ttl time.Duration) error {
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, pendingKey(userID), data, ttl).Err()
}

// GetPendingAction returns ok=false if there is none or it has expired.
func GetPendingAction(ctx context.Context, rdb *redis.Client, userID int64) (PendingAction, bool, error) {
	data, err := rdb.Get(ctx, pendingKey(userID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return PendingAction{}, false, nil
	}
	if err != nil {
		return PendingAction{}, false, err
	}
	var p PendingAction
	if err := json.Unmarshal(data, &p); err != nil {
		return PendingAction{}, false, err
	}
	return p, true, nil
}

// ClearPendingAction removes a pending action once it's been executed or
// abandoned (see plan decision #3's "trader moving on" case).
func ClearPendingAction(ctx context.Context, rdb *redis.Client, userID int64) error {
	return rdb.Del(ctx, pendingKey(userID)).Err()
}
