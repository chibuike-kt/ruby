package whatsapp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/chibuike-kt/ruby/internal/account"
	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/idempotency"
)

const (
	directionInbound  = "INBOUND"
	statusReceived    = "RECEIVED"
	statusUnknownUser = "UNKNOWN_USER"

	// dedupTTL matches the "a few minutes" guidance from
	// docs/BRIEF-api-redis.md's original webhook dedup fast-path spec.
	dedupTTL = 5 * time.Minute
)

// ErrMalformedPayload wraps a webhook body that couldn't be parsed or
// didn't contain the fields required to process it. Callers map this
// to 400; anything else ReceiveEvent returns is an infrastructure
// failure worth a 500 (and worth letting Meta's retry recover from).
var ErrMalformedPayload = errors.New("whatsapp: malformed webhook payload")

type Service struct {
	pool        *pgxpool.Pool
	redis       *redis.Client
	appSecret   string
	verifyToken string
	logger      *slog.Logger
}

func NewService(pool *pgxpool.Pool, redisClient *redis.Client, appSecret, verifyToken string, logger *slog.Logger) *Service {
	return &Service{pool: pool, redis: redisClient, appSecret: appSecret, verifyToken: verifyToken, logger: logger}
}

// VerifySignature checks the POST body's X-Hub-Signature-256 header
// against this service's configured app secret.
func (s *Service) VerifySignature(body []byte, header string) bool {
	return VerifySignature(s.appSecret, body, header)
}

// VerifyHandshake checks Meta's one-time GET verification handshake:
// hub.mode must be "subscribe" and hub.verify_token must match the
// configured token. An empty configured token always fails — same
// fail-closed reasoning as VerifySignature.
func (s *Service) VerifyHandshake(mode, token string) bool {
	if s.verifyToken == "" {
		return false
	}
	return mode == "subscribe" && subtle.ConstantTimeCompare([]byte(token), []byte(s.verifyToken)) == 1
}

// Outcome reports what happened to one inbound message — mainly for
// tests to assert on; the HTTP handler only needs to know whether
// ReceiveEvent returned an error.
type Outcome struct {
	ProviderMessageID string
	MessageType       string
	Duplicate         bool
	UnknownSender     bool
	Stored            bool
}

// ReceiveEvent parses, dedupes, and stores every inbound message in a
// webhook delivery (spec §15/§31, decisions.md #2). It returns once
// messages are durably stored — the stub "processing" step is handed
// off to a goroutine so the caller can acknowledge Meta immediately
// after this returns, per the Cloud API's fast-ack requirement.
func (s *Service) ReceiveEvent(ctx context.Context, body []byte) ([]Outcome, error) {
	var payload WebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}

	var outcomes []Outcome
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, msg := range change.Value.Messages {
				outcome, err := s.receiveMessage(ctx, msg)
				if err != nil {
					return outcomes, err
				}
				outcomes = append(outcomes, outcome)
			}
		}
	}
	return outcomes, nil
}

func (s *Service) receiveMessage(ctx context.Context, msg Message) (Outcome, error) {
	outcome := Outcome{ProviderMessageID: msg.ID, MessageType: msg.Type}

	if msg.ID == "" {
		return outcome, fmt.Errorf("%w: message missing id", ErrMalformedPayload)
	}

	// Redis fast-path (decisions.md #2): a miss (down, evicted, or the
	// check itself erroring) is never read as "definitely new" — the
	// unique index on messages.provider_message_id, caught in the
	// insert's error branch below, is the real guarantee.
	if seenBefore, err := idempotency.CheckAndMark(ctx, s.redis, msg.ID, dedupTTL); err == nil && seenBefore {
		outcome.Duplicate = true
		return outcome, nil
	}

	var userID *int64
	a, err := account.GetByPhoneNumber(ctx, s.pool, normalizePhone(msg.From))
	switch {
	case err == nil:
		userID = &a.ID
	case errors.Is(err, account.ErrNotFound):
		outcome.UnknownSender = true
	default:
		return outcome, err
	}

	status := statusReceived
	if outcome.UnknownSender {
		status = statusUnknownUser
	}

	stored, err := insertMessage(ctx, s.pool, StoredMessage{
		UserID:            userID,
		ProviderMessageID: msg.ID,
		Direction:         directionInbound,
		MessageType:       msg.Type,
		ContentReference:  contentReference(msg),
		ProcessingStatus:  status,
	})
	if err != nil {
		if db.IsUniqueViolation(err, providerMessageIDConstraint) {
			// Redis missed it, but the actual guarantee caught it.
			outcome.Duplicate = true
			return outcome, nil
		}
		return outcome, err
	}

	outcome.Stored = true
	go s.handOff(stored)

	return outcome, nil
}

func contentReference(msg Message) *string {
	switch msg.Type {
	case "text":
		if msg.Text != nil {
			return &msg.Text.Body
		}
	case "audio":
		if msg.Audio != nil {
			return &msg.Audio.ID
		}
	}
	return nil
}

// normalizePhone matches WhatsApp's digits-only "from" field (e.g.
// "16505551234") against this system's E.164-with-"+" convention for
// users.phone_number (e.g. "+2348012345678", per spec §4's own
// example and every fixture in this codebase).
func normalizePhone(from string) string {
	from = strings.TrimSpace(from)
	if from == "" || strings.HasPrefix(from, "+") {
		return from
	}
	return "+" + from
}

// handOff stands in for real intent extraction (a later slice).
// Unlike the literal "logs 'would process: {content}'" phrasing this
// slice's brief uses, this deliberately never logs the message body or
// sender's number — spec §36 forbids exactly that, and being a stub
// doesn't exempt it.
func (s *Service) handOff(msg StoredMessage) {
	if s.logger == nil {
		return
	}
	contentLength := 0
	if msg.ContentReference != nil {
		contentLength = len(*msg.ContentReference)
	}
	s.logger.Info("would process message",
		"provider_message_id", msg.ProviderMessageID,
		"type", msg.MessageType,
		"content_length", contentLength,
	)
}
