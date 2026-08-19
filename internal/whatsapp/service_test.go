package whatsapp_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/chibuike-kt/ruby/internal/dbtest"
	"github.com/chibuike-kt/ruby/internal/whatsapp"
)

func newTestService(t *testing.T) *whatsapp.Service {
	t.Helper()
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	return whatsapp.NewService(pool, rdb, "test-secret", "test-verify-token", slog.Default())
}

func textPayload(from, id, body string) []byte {
	return fmt.Appendf(nil, `{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "entry-1",
			"changes": [{
				"field": "messages",
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "15550783881", "phone_number_id": "106540352242922"},
					"contacts": [{"profile": {"name": "Trader"}, "wa_id": %q}],
					"messages": [{"from": %q, "id": %q, "timestamp": "1749416383", "type": "text", "text": {"body": %q}}]
				}
			}]
		}]
	}`, from, from, id, body)
}

func audioPayload(from, id, mediaID string) []byte {
	return fmt.Appendf(nil, `{
		"object": "whatsapp_business_account",
		"entry": [{
			"id": "entry-1",
			"changes": [{
				"field": "messages",
				"value": {
					"messaging_product": "whatsapp",
					"metadata": {"display_phone_number": "15550783881", "phone_number_id": "106540352242922"},
					"contacts": [{"profile": {"name": "Trader"}, "wa_id": %q}],
					"messages": [{"from": %q, "id": %q, "timestamp": "1749416383", "type": "audio", "audio": {"id": %q, "mime_type": "audio/ogg"}}]
				}
			}]
		}]
	}`, from, from, id, mediaID)
}

func TestVerifyHandshake(t *testing.T) {
	svc := newTestService(t)

	if !svc.VerifyHandshake("subscribe", "test-verify-token") {
		t.Fatal("got false for the correct mode and token, want true")
	}
}

func TestVerifyHandshake_WrongToken(t *testing.T) {
	svc := newTestService(t)

	if svc.VerifyHandshake("subscribe", "wrong-token") {
		t.Fatal("got true for the wrong token, want false")
	}
}

func TestVerifyHandshake_WrongMode(t *testing.T) {
	svc := newTestService(t)

	if svc.VerifyHandshake("unsubscribe", "test-verify-token") {
		t.Fatal("got true for a mode other than subscribe, want false")
	}
}

func TestReceiveEvent_KnownSender_TextMessage(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	svc := whatsapp.NewService(pool, rdb, "test-secret", "test-verify-token", slog.Default())
	userID := dbtest.CreateUser(t, pool, "+16505551234")

	outcomes, err := svc.ReceiveEvent(context.Background(), textPayload("16505551234", "wamid.text1", "Chinedu took 2 cartons of noodles for 75k"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(outcomes))
	}
	o := outcomes[0]
	if !o.Stored || o.Duplicate || o.UnknownSender {
		t.Fatalf("got outcome %+v, want a fresh stored message for a known sender", o)
	}
	if o.MessageType != "text" {
		t.Fatalf("got type %q, want text", o.MessageType)
	}

	var storedUserID int64
	var contentRef, status string
	err = pool.QueryRow(context.Background(),
		`SELECT user_id, content_reference, processing_status FROM messages WHERE provider_message_id = $1`,
		"wamid.text1").Scan(&storedUserID, &contentRef, &status)
	if err != nil {
		t.Fatalf("query stored message: %v", err)
	}
	if storedUserID != userID {
		t.Fatalf("got user_id %d, want %d", storedUserID, userID)
	}
	if contentRef != "Chinedu took 2 cartons of noodles for 75k" {
		t.Fatalf("got content_reference %q, want the text body", contentRef)
	}
	if status != "RECEIVED" {
		t.Fatalf("got processing_status %q, want RECEIVED", status)
	}
}

func TestReceiveEvent_KnownSender_AudioMessage_StoresMediaID(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	svc := whatsapp.NewService(pool, rdb, "test-secret", "test-verify-token", slog.Default())
	dbtest.CreateUser(t, pool, "+16505551235")

	outcomes, err := svc.ReceiveEvent(context.Background(), audioPayload("16505551235", "wamid.audio1", "media-abc-123"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Stored {
		t.Fatalf("got outcomes %+v, want a single stored message", outcomes)
	}

	var contentRef, messageType string
	err = pool.QueryRow(context.Background(),
		`SELECT content_reference, message_type FROM messages WHERE provider_message_id = $1`,
		"wamid.audio1").Scan(&contentRef, &messageType)
	if err != nil {
		t.Fatalf("query stored message: %v", err)
	}
	if contentRef != "media-abc-123" {
		t.Fatalf("got content_reference %q, want the media id (spec: no transcription in this slice)", contentRef)
	}
	if messageType != "audio" {
		t.Fatalf("got message_type %q, want audio", messageType)
	}
}

func TestReceiveEvent_PhoneNormalization_MissingPlus(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	svc := whatsapp.NewService(pool, rdb, "test-secret", "test-verify-token", slog.Default())
	// Stored the way every other fixture in this codebase stores it —
	// E.164 with a leading "+" — while WhatsApp's "from" field never
	// includes one.
	dbtest.CreateUser(t, pool, "+2348012345678")

	outcomes, err := svc.ReceiveEvent(context.Background(), textPayload("2348012345678", "wamid.normalize1", "hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 || outcomes[0].UnknownSender {
		t.Fatalf("got outcomes %+v, want the sender resolved despite the missing '+' in the raw payload", outcomes)
	}
}

func TestReceiveEvent_UnknownSender_StillStoresMessage(t *testing.T) {
	svc := newTestService(t)

	outcomes, err := svc.ReceiveEvent(context.Background(), textPayload("19995550000", "wamid.unknown1", "hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(outcomes))
	}
	o := outcomes[0]
	if !o.UnknownSender || !o.Stored {
		t.Fatalf("got outcome %+v, want UnknownSender=true and Stored=true", o)
	}
}

func TestReceiveEvent_Duplicate_SecondCallDoesNotCreateSecondRow(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	svc := whatsapp.NewService(pool, rdb, "test-secret", "test-verify-token", slog.Default())
	dbtest.CreateUser(t, pool, "+16505551236")

	payload := textPayload("16505551236", "wamid.dup1", "hello")

	first, err := svc.ReceiveEvent(context.Background(), payload)
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if !first[0].Stored || first[0].Duplicate {
		t.Fatalf("first call: got %+v, want a fresh stored message", first[0])
	}

	second, err := svc.ReceiveEvent(context.Background(), payload)
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if !second[0].Duplicate {
		t.Fatalf("second call: got %+v, want Duplicate=true", second[0])
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM messages WHERE provider_message_id = $1`, "wamid.dup1").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d rows for the same provider_message_id, want exactly 1", count)
	}
}

// The true guarantee (decisions.md #2): even if the Redis fast-path
// never saw this message id (simulated here by flushing the key
// straight after CheckAndMark would have set it — i.e. skipping the
// dedup pre-check's memory entirely isn't possible through the public
// API, so instead this drives two ReceiveEvent calls concurrently,
// racing both past the Redis check before either has inserted into
// Postgres), Postgres's unique index is what actually prevents a
// second row.
func TestReceiveEvent_ConcurrentDuplicate_PostgresStillCatchesIt(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	svc := whatsapp.NewService(pool, rdb, "test-secret", "test-verify-token", slog.Default())
	dbtest.CreateUser(t, pool, "+16505551237")

	payload := textPayload("16505551237", "wamid.race1", "hello")

	type result struct {
		outcomes []whatsapp.Outcome
		err      error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			o, err := svc.ReceiveEvent(context.Background(), payload)
			results <- result{o, err}
		}()
	}

	for range 2 {
		r := <-results
		if r.err != nil {
			t.Fatalf("unexpected error: %v", r.err)
		}
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM messages WHERE provider_message_id = $1`, "wamid.race1").Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d rows from a concurrent duplicate delivery, want exactly 1", count)
	}
}

func TestReceiveEvent_MalformedJSON(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.ReceiveEvent(context.Background(), []byte(`{not valid json`))
	if !errors.Is(err, whatsapp.ErrMalformedPayload) {
		t.Fatalf("got %v, want ErrMalformedPayload", err)
	}
}

func TestReceiveEvent_MessageMissingID(t *testing.T) {
	svc := newTestService(t)

	body := []byte(`{
		"object": "whatsapp_business_account",
		"entry": [{"id": "e1", "changes": [{"field": "messages", "value": {
			"messaging_product": "whatsapp",
			"metadata": {"display_phone_number": "1", "phone_number_id": "1"},
			"messages": [{"from": "16505551234", "id": "", "timestamp": "1", "type": "text", "text": {"body": "hi"}}]
		}}]}]
	}`)

	_, err := svc.ReceiveEvent(context.Background(), body)
	if !errors.Is(err, whatsapp.ErrMalformedPayload) {
		t.Fatalf("got %v, want ErrMalformedPayload for a message with no id", err)
	}
}

func TestReceiveEvent_NoMessagesInPayload_NoOutcomesNoError(t *testing.T) {
	svc := newTestService(t)

	// A status-update delivery (sent/delivered/read) — this slice only
	// processes inbound messages, so this must be a harmless no-op.
	body := []byte(`{
		"object": "whatsapp_business_account",
		"entry": [{"id": "e1", "changes": [{"field": "messages", "value": {
			"messaging_product": "whatsapp",
			"metadata": {"display_phone_number": "1", "phone_number_id": "1"}
		}}]}]
	}`)

	outcomes, err := svc.ReceiveEvent(context.Background(), body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("got %d outcomes, want 0", len(outcomes))
	}
}

func TestReceiveEvent_UnsupportedMessageType_StoredWithoutContentReference(t *testing.T) {
	pool := dbtest.Open(t)
	rdb := dbtest.OpenRedis(t)
	svc := whatsapp.NewService(pool, rdb, "test-secret", "test-verify-token", slog.Default())
	dbtest.CreateUser(t, pool, "+16505551238")

	body := []byte(`{
		"object": "whatsapp_business_account",
		"entry": [{"id": "e1", "changes": [{"field": "messages", "value": {
			"messaging_product": "whatsapp",
			"metadata": {"display_phone_number": "1", "phone_number_id": "1"},
			"messages": [{"from": "16505551238", "id": "wamid.image1", "timestamp": "1", "type": "image"}]
		}}]}]
	}`)

	outcomes, err := svc.ReceiveEvent(context.Background(), body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(outcomes) != 1 || !outcomes[0].Stored {
		t.Fatalf("got outcomes %+v, want a stored (not crashed/rejected) message", outcomes)
	}
}
