package customer

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultLastCustomerContextTTL is how long a "last customer" hint
// stays valid (spec §8 signal 4) before Ruby must ask again rather than
// silently assuming a stale conversation is still current.
const DefaultLastCustomerContextTTL = 10 * time.Minute

func lastCustomerContextKey(userID int64) string {
	return fmt.Sprintf("ruby:ctx:%d:last_customer", userID)
}

// SetLastCustomerContext records the customer a debt or payment was
// just recorded for, so a later ambiguous reference ("he paid me 30k")
// can be resolved without asking again (spec §8 signal 4). This is a
// conversational hint, not a financial record: callers set it as a
// best-effort side effect after a Slice 1 mutation has already
// committed — a Redis outage here must never affect the ledger.
func SetLastCustomerContext(ctx context.Context, rdb *redis.Client, userID, customerID int64, ttl time.Duration) error {
	return rdb.Set(ctx, lastCustomerContextKey(userID), customerID, ttl).Err()
}

// GetLastCustomerContext returns the last customer_id recorded for
// userID, or ok=false if there is none or it has expired.
func GetLastCustomerContext(ctx context.Context, rdb *redis.Client, userID int64) (customerID int64, ok bool, err error) {
	val, err := rdb.Get(ctx, lastCustomerContextKey(userID)).Result()
	if errors.Is(err, redis.Nil) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	id, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}
