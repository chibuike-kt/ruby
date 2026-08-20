# Brief — typing indicators, professional copy, interactive polish, security hardening

Five areas, each with concrete, specific direction, not open-ended judgment calls. This is about making Ruby feel like a finished product in a live demo, not just correct in a test suite.

## 1. Typing indicator + read receipt on every inbound message

Confirmed current Cloud API shape, a single combined call:

```
POST /{PHONE_NUMBER_ID}/messages
{
  "messaging_product": "whatsapp",
  "status": "read",
  "message_id": "<the incoming message's wamid>",
  "typing_indicator": { "type": "text" }
}
```

Call this immediately in the webhook handler, before dispatching to the AI pipeline, for every inbound message Ruby is actually going to respond to (not for a message that gets silently deduped, per the existing idempotency logic). This is the very first thing that happens after signature verification and dedup, before anything else.

Notes:
- The indicator auto-dismisses after 25 seconds or on your actual reply, whichever is first. If a request could realistically take longer than that (a slow OpenAI round trip, retries), it's fine for the indicator to lapse before the reply lands, don't build special handling to re-trigger it, that's over-engineering for a hackathon timeline.
- Add a test confirming this call fires for a normal inbound message and does not fire for a message that gets deduped before reaching the AI pipeline.

## 2. Replace the capability-list copy with this exact text

Current wording reads casual and unstructured. Replace it with this, adapted per language using the same translation quality bar already applied elsewhere, not just English:

```
Here's what I can help you manage:

*Record a sale on credit*
e.g. "Chinedu took 5k, pays Friday"

*Log a payment*
e.g. "Chinedu paid 5k"

*Review outstanding balances*
e.g. "Who owes me?"

*Check a specific customer*
e.g. "How much does Chinedu owe?"

Tell me in your own words, I'll take it from there.
```

This replaces the capability list wherever it currently appears (HELP intent, the fallback for genuinely unsupported requests per decisions around 1c, the greeting flow's explanation). Use it consistently, don't let different code paths drift into slightly different wording over time.

## 3. Interactive buttons on every flow that has a small, fixed answer set

Audit every multi-step flow and confirm each one below has real buttons, not free text, matching the pattern already built for the greeting menu and disambiguation:

- **Reminder opt-in** ("Want me to remind Chinedu before this is due?") → Yes / No buttons.
- **Identity confirmation** (decision #9, same-or-new customer) → Same person / New person buttons.
- **Overpayment confirmation** → Confirm / Edit / Cancel buttons (already built, confirm it still applies correctly to records completed via the slot-filling flow, not only fully-parsed single-message records).
- **The capability-list / HELP response** → attach the same three quick-action buttons used in the greeting menu (Record a debt / Who owes me? / Help), for visual consistency every time this content appears, not just on first contact.
- **Slot-filling questions** (asking for a missing amount or customer identity) stay free text, since the answer space is genuinely open, but attach a single Cancel button alongside the question so backing out of an in-progress record is one tap instead of typing "cancel" or a language-specific equivalent.

Every button-driven flow needs its free-text fallback to keep working exactly as before, buttons are additive, never a replacement for typing the answer.

## 4. Security and systems design hardening pass

Concrete checklist, verify each item directly rather than asserting it's fine:

- **Every Redis key written by any pending-state flow has an explicit TTL.** Audit every `SET`/equivalent call across onboarding, confirmation, disambiguation, and slot-filling, a key with no expiry is a slow memory leak and a source of stale-state bugs weeks from now.
- **No raw SQL string concatenation anywhere.** Confirm every query goes through parameterized queries via the existing pgx patterns, a quick grep for string formatting near SQL keywords is enough to confirm this holds.
- **Outbound calls to OpenAI and the WhatsApp API have real timeouts and retry/backoff**, a hung upstream call must not hang the goroutine processing a message indefinitely.
- **No secret ever reaches a log line.** This session added meaningfully more error-level logging, specifically re-check those new log statements for accidentally including a raw request body, header, or token rather than just the error message.
- **The reminder dispatcher's periodic polling doesn't create lock contention with normal request traffic.** Confirm it uses a pattern like `SELECT ... FOR UPDATE SKIP LOCKED` (or equivalent) so a background scan never blocks a real trader's request.
- **Database connection pool sizing accounts for the new background dispatcher goroutine running concurrently with request handling.** Confirm the pool's max connections is still sane given this new steady background consumer.
- **Rate limiting is still active on every mutation endpoint** after everything added this session, this was built early on, confirm it wasn't inadvertently bypassed by a new code path.

Report findings directly, if everything already holds, say so plainly rather than padding the report, if something's found, fix it and note what was actually wrong.

## 5. General demo polish

Beyond the specific items above, the standing bar for anything user facing from here on: every response should look composed, not like raw text output. This means consistent use of the formatting already established (bold for amounts and names, line breaks between distinct pieces of information), buttons wherever the answer space allows it per section 3, and the typing indicator from section 1 on every exchange. If you're building a new response path and it's tempting to just return a plain string, that's the signal to stop and apply the same treatment the rest of the product already has, not a one-off exception.

## Testing summary

- Typing indicator fires on normal inbound messages, not on deduped ones.
- The new capability-list copy renders correctly with buttons attached, in at least English and one other supported language.
- Each flow in section 3 has both a button-tap test and a free-text-fallback test.
- Security checklist items each have either a passing test or a direct confirmation in the report.
