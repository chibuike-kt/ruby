package whatsapp

import (
	"context"
	"time"

	"github.com/chibuike-kt/ruby/internal/db"
)

// providerMessageIDConstraint names the unique index backing spec
// §15/§31's actual guarantee — see migrations/000001_init.up.sql. The
// Redis fast-path in internal/idempotency is an optimization on top of
// this, never a replacement for it.
const providerMessageIDConstraint = "idx_messages_provider_message_id"

type StoredMessage struct {
	ID                int64
	UserID            *int64
	ProviderMessageID string
	Direction         string
	MessageType       string
	ContentReference  *string
	ProcessingStatus  string
	CreatedAt         time.Time
}

func insertMessage(ctx context.Context, q db.Querier, m StoredMessage) (StoredMessage, error) {
	row := q.QueryRow(ctx, `
		INSERT INTO messages (user_id, provider_message_id, direction, message_type, content_reference, processing_status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, user_id, provider_message_id, direction, message_type, content_reference, processing_status, created_at
	`, m.UserID, m.ProviderMessageID, m.Direction, m.MessageType, m.ContentReference, m.ProcessingStatus)

	var stored StoredMessage
	err := row.Scan(&stored.ID, &stored.UserID, &stored.ProviderMessageID, &stored.Direction,
		&stored.MessageType, &stored.ContentReference, &stored.ProcessingStatus, &stored.CreatedAt)
	return stored, err
}
