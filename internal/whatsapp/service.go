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
	"github.com/chibuike-kt/ruby/internal/ai"
	"github.com/chibuike-kt/ruby/internal/db"
	"github.com/chibuike-kt/ruby/internal/idempotency"
)

const (
	directionInbound  = "INBOUND"
	directionOutbound = "OUTBOUND"
	statusReceived    = "RECEIVED"
	statusSent        = "SENT"

	// dedupTTL matches the "a few minutes" guidance from
	// docs/BRIEF-api-redis.md's original webhook dedup fast-path spec.
	dedupTTL = 5 * time.Minute
)

// ErrMalformedPayload wraps a webhook body that couldn't be parsed or
// didn't contain the fields required to process it. Callers map this
// to 400; anything else ReceiveEvent returns is an infrastructure
// failure worth a 500 (and worth letting Meta's retry recover from).
var ErrMalformedPayload = errors.New("whatsapp: malformed webhook payload")

// Handler is invoked, off the request path, once a message is durably
// stored — the real counterpart to what handOff used to stub out. A nil
// Handler (the default) leaves Service running receive/verify/dedupe/store
// only, exactly as before this field existed.
type Handler func(ctx context.Context, msg StoredMessage)

type Service struct {
	pool          *pgxpool.Pool
	redis         *redis.Client
	appSecret     string
	verifyToken   string
	accessToken   string
	phoneNumberID string
	logger        *slog.Logger
	handler       Handler
}

func NewService(pool *pgxpool.Pool, redisClient *redis.Client, appSecret, verifyToken, accessToken, phoneNumberID string, logger *slog.Logger) *Service {
	return &Service{
		pool:          pool,
		redis:         redisClient,
		appSecret:     appSecret,
		verifyToken:   verifyToken,
		accessToken:   accessToken,
		phoneNumberID: phoneNumberID,
		logger:        logger,
	}
}

// SetHandler wires the real message-processing pipeline in. It's a
// setter rather than a constructor argument because the handler
// typically needs a *Service back (to send the reply) — see
// cmd/api/main.go for how the two are tied together without an import
// cycle. Safe to call once, before the server starts accepting requests;
// not safe to call concurrently with an in-flight ReceiveEvent.
func (s *Service) SetHandler(h Handler) {
	s.handler = h
}

// SendText sends a text reply and durably records it as an outbound
// message (spec §39 audit trail), mirroring how an inbound message is
// stored.
func (s *Service) SendText(ctx context.Context, to, body string) error {
	id, err := sendText(ctx, s.accessToken, s.phoneNumberID, to, body)
	if err != nil {
		return err
	}
	return s.recordOutbound(ctx, to, id, "text", body)
}

// SendButtons sends up to 3 reply buttons alongside body — the
// disambiguation/confirmation upgrade from
// docs/BRIEF-interactive-messages.md. buttons is []ai.Button rather
// than a whatsapp-local type because ai.Sender (satisfied structurally
// by *Service) already defines the shape it needs; whatsapp importing
// ai here is one-directional and doesn't reintroduce the construction-
// order cycle SetHandler exists to break (ai still never imports
// whatsapp).
func (s *Service) SendButtons(ctx context.Context, to, body string, buttons []ai.Button) error {
	id, err := sendInteractiveButtons(ctx, s.accessToken, s.phoneNumberID, to, body, buttons)
	if err != nil {
		return err
	}
	return s.recordOutbound(ctx, to, id, "interactive", body)
}

// SendList sends a WhatsApp list message (4-10 options) alongside body.
func (s *Service) SendList(ctx context.Context, to, body, buttonLabel string, sections []ai.ListSection) error {
	id, err := sendInteractiveList(ctx, s.accessToken, s.phoneNumberID, to, body, buttonLabel, sections)
	if err != nil {
		return err
	}
	return s.recordOutbound(ctx, to, id, "interactive", body)
}

// recordOutbound is shared by every Send* method. The account lookup is
// best-effort: a failure to attribute the row to a user_id doesn't stop
// the reply from being recorded.
func (s *Service) recordOutbound(ctx context.Context, to, providerMessageID, messageType, body string) error {
	var userID *int64
	if a, acctErr := account.GetByPhoneNumber(ctx, s.pool, to); acctErr == nil {
		userID = &a.ID
	}

	_, err := insertMessage(ctx, s.pool, StoredMessage{
		UserID:            userID,
		ProviderMessageID: providerMessageID,
		Direction:         directionOutbound,
		MessageType:       messageType,
		ContentReference:  &body,
		ProcessingStatus:  statusSent,
	})
	return err
}

// SendTemplate sends a Meta-approved WhatsApp template message and
// durably records it as an outbound message, mirroring SendText —
// required for reminders (docs/BRIEF-fixes-and-reminders.md #4): the
// customer has never messaged Ruby, so this is a business-initiated
// conversation from message one, which the Cloud API only allows via an
// approved template, never freeform text.
func (s *Service) SendTemplate(ctx context.Context, to, templateName, languageCode string, bodyParams []string) (string, error) {
	id, err := sendTemplate(ctx, s.accessToken, s.phoneNumberID, to, templateName, languageCode, bodyParams)
	if err != nil {
		return "", err
	}
	body := fmt.Sprintf("[template:%s]", templateName)
	if err := s.recordOutbound(ctx, to, id, "template", body); err != nil {
		return "", err
	}
	return id, nil
}

// DownloadMedia fetches the bytes behind a WhatsApp media id (spec §22).
func (s *Service) DownloadMedia(ctx context.Context, mediaID string) ([]byte, string, error) {
	return downloadMedia(ctx, s.accessToken, mediaID)
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

	userID, err := s.resolveOrCreateUser(ctx, msg.From)
	if err != nil {
		return outcome, err
	}

	stored, err := insertMessage(ctx, s.pool, StoredMessage{
		UserID:            &userID,
		ProviderMessageID: msg.ID,
		Direction:         directionInbound,
		MessageType:       msg.Type,
		ContentReference:  contentReference(msg),
		ProcessingStatus:  statusReceived,
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
	go s.handOff(context.WithoutCancel(ctx), stored)

	return outcome, nil
}

// resolveOrCreateUser looks up the sender's account, auto-creating one
// with an empty name on first contact (docs/BRIEF-response-quality.md
// #1) — Ruby's whole premise is zero-friction onboarding, so a message
// from an unrecognized phone number claims the number immediately
// rather than sitting unprocessable until someone manually registers
// it. internal/ai's name-capture flow asks for the name and fills it
// in; this layer only needs the account to exist.
func (s *Service) resolveOrCreateUser(ctx context.Context, from string) (int64, error) {
	phone := normalizePhone(from)
	a, err := account.GetByPhoneNumber(ctx, s.pool, phone)
	if errors.Is(err, account.ErrNotFound) {
		a, err = account.Create(ctx, s.pool, phone)
	}
	if err != nil {
		return 0, err
	}
	return a.ID, nil
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
	case "interactive":
		if msg.Interactive == nil {
			return nil
		}
		if msg.Interactive.ButtonReply != nil {
			return &msg.Interactive.ButtonReply.ID
		}
		if msg.Interactive.ListReply != nil {
			return &msg.Interactive.ListReply.ID
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

// handOff dispatches a durably-stored message to the real processing
// pipeline (internal/ai, wired in via SetHandler). Every inbound
// message now carries a user_id — resolveOrCreateUser guarantees the
// account exists before insertMessage ever runs — so there's no
// "unrecognized sender" case left to skip. With no handler configured,
// this falls back to logging that a message arrived — deliberately
// never the message body or sender's number, since spec §36 forbids
// exactly that and being a fallback doesn't exempt it.
func (s *Service) handOff(ctx context.Context, msg StoredMessage) {
	if s.handler != nil {
		s.handler(ctx, msg)
		return
	}

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
