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

// PendingAction is a RawIntent parked in Redis awaiting one more piece of
// trader input before Processor executes it — either a confirmation or a
// disambiguation reply (plan decision #3). Kind, not a separate store per
// case, since both are "the same intent, needs one more answer."
type PendingAction struct {
	Kind       PendingKind
	Intent     RawIntent
	Candidates []PendingCandidateOption
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
