# Brief — WhatsApp webhook

Builds on the financial core and API layer, both verified end-to-end
over real HTTP (customer creation, debt creation, idempotent partial
payment, ledger integrity, duplicate-name guard all confirmed
manually). This slice adds the actual WhatsApp Cloud API webhook —
pure infrastructure, no AI yet. That's the next slice after this one.

## Scope

`internal/whatsapp` — receive, verify, dedupe, and store inbound
WhatsApp events. Do not build intent extraction, response generation,
or any AI call in this slice. The webhook's job ends at "message
safely stored, ready for processing" — hand off to a stub that just
logs "would process: {content}" for now.

## Endpoints (Meta Cloud API contract)

- `GET /api/webhooks/whatsapp` — verification handshake. Meta calls
  this once when the webhook URL is configured, with `hub.mode`,
  `hub.verify_token`, `hub.challenge` query params. Compare
  `hub.verify_token` against `WHATSAPP_WEBHOOK_VERIFY_TOKEN` (already
  in .env.example); if it matches, echo back `hub.challenge` as plain
  text with 200. If not, 403.

- `POST /api/webhooks/whatsapp` — actual event delivery. This is the
  one that matters. Steps, in order:

  1. **Verify the request signature** before touching the body.
     Meta signs with `X-Hub-Signature-256` (HMAC-SHA256 of the raw
     body, using `WHATSAPP_APP_SECRET` as the key). Reject with 403
     on mismatch or missing header — don't process an unsigned
     request under any circumstance, this is the actual security
     boundary per spec §35.
  2. **Acknowledge fast.** Meta expects a 200 quickly or it'll retry
     (sometimes aggressively) and can eventually disable the webhook
     if it times out repeatedly. Parse and validate structure, dedupe
     check, persist to `messages`, return 200 — then hand off
     processing asynchronously (a goroutine is fine for this slice;
     don't build a full queue infrastructure unless something here
     genuinely needs it).
  3. **Dedupe using what's already built.** Every inbound message has
     a `provider_message_id` (WhatsApp's `messages[].id` field). Use
     `internal/idempotency`'s existing dedup check — Redis fast-path,
     Postgres unique index as the real guarantee (decisions.md #2).
     If it's a duplicate, still return 200 (Meta shouldn't retry
     forever) but don't reprocess.
  4. **Identify the user.** Look up by the sender's WhatsApp number
     (`messages[].from`) against `users.phone_number`. If no match,
     this is a new/unknown sender — for this slice, just log it and
     store the message with `processing_status = 'UNKNOWN_USER'`.
     Don't build onboarding in this slice; that's a real feature
     (account creation flow) worth its own scoped brief later.
  5. **Store the message** in `messages` per the existing schema —
     `provider_message_id`, `direction = 'INBOUND'`, `message_type`
     (text/audio/etc, from the payload), `content_reference` (store
     the raw text for text messages; for voice notes, store the
     WhatsApp media ID — actual audio retrieval/transcription is a
     separate slice, don't build it here).

## What this slice explicitly does NOT do

- No AI intent extraction (next slice).
- No response sent back to the trader (next slice — this one is
  receive-only).
- No voice note download/transcription (store the media ID, stop
  there).
- No account/onboarding flow for unknown senders.
- No customer resolution or conversational context consumption yet
  (the Redis context store from the API/Redis slice is ready but
  unused until AI intent extraction needs it).

## Testing

- Signature verification: valid signature accepted, invalid/missing
  rejected with 403, using a real HMAC computed the same way Meta
  does (don't just check for the header's presence).
- Verification handshake: correct token echoes challenge, wrong
  token 403s.
- Dedup: same `provider_message_id` sent twice → second call doesn't
  create a second `messages` row (reuse the existing dedup test
  patterns from `internal/idempotency`).
- Unknown sender: message still gets stored with the right status,
  doesn't crash or 500.
- Malformed payload: doesn't panic, returns an appropriate error
  without leaking internals (spec §37).

## A note on the extra time

There's more runway now than when the earlier slices were rushed —
worth actually reading Meta's Cloud API webhook payload docs directly
rather than working from memory of the shape, since webhook payload
formats are exactly the kind of thing that drifts by minor version
and a wrong assumption here is expensive to debug blind (we've
already burned real time chasing assumption-based bugs this session).
