// Package idempotency provides the Redis-backed fast-path dedup check
// for inbound webhook events (spec §15/§31). No webhook exists yet —
// that's Slice 2's internal/whatsapp — this just builds the reusable
// check so Slice 2 can call it directly.
package idempotency

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const webhookKeyPrefix = "ruby:wh:"

var ErrProviderMessageIDRequired = errors.New("idempotency: provider message id is required")

// CheckAndMark reports whether providerMessageID has already been seen
// within ttl, using SETNX as a cheap pre-check before Postgres
// (decisions.md #2). It is an optimization only: the messages table's
// unique index on provider_message_id remains the actual source of
// truth. A Redis error must never be read by the caller as "definitely
// new" — it only means "fall back to Postgres," which still rejects a
// real duplicate insert on its own.
func CheckAndMark(ctx context.Context, rdb *redis.Client, providerMessageID string, ttl time.Duration) (seenBefore bool, err error) {
	if providerMessageID == "" {
		return false, ErrProviderMessageIDRequired
	}
	ok, err := rdb.SetNX(ctx, webhookKeyPrefix+providerMessageID, 1, ttl).Result()
	if err != nil {
		return false, err
	}
	return !ok, nil
}
