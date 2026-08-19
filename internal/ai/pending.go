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

	// OriginalMessage and ReaskCount are used by PendingAwaitingName only.
	OriginalMessage *PendingOriginalMessage
	ReaskCount      int
}

// DefaultPendingTTL matches the conversational-context window the rest
// of Ruby already uses — after this long without a reply, Ruby should
// ask fresh rather than resume a stale exchange.
const DefaultPendingTTL = customer.DefaultLastCustomerContextTTL

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
