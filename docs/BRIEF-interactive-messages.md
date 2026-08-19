# Brief — Interactive messages (buttons/lists)

Adds tappable WhatsApp UI to flows that already exist and already work
over plain text — this is a UX upgrade, not new logic. Don't touch
the underlying disambiguation/confirmation logic itself, only how it's
presented.

## API shape, confirmed current

`POST /{phone_number_id}/messages`, `"type": "interactive"`:

- **Reply buttons** (`"interactive.type": "button"`) — max 3 buttons,
  each `{"type": "reply", "reply": {"id": "...", "title": "..."}}`,
  title capped at 20 characters (25 is the hard limit but 20 avoids
  truncation on small screens).
- **List messages** (`"interactive.type": "list"`) — up to 10 options
  in sections, for when there are more than 3 choices.

Inbound replies to these arrive as `type: "interactive"` webhook
events with `interactive.type` of `button_reply` or `list_reply`,
carrying the `id` you set — not free text. `internal/whatsapp`'s
inbound parsing needs to handle this alongside the existing text/audio
message types.

## Flows to convert

**1. Customer disambiguation** (already built, currently a text
prompt with numbered options) — convert to:
- ≤3 candidates → reply buttons, one per candidate. Button `id` should
  be the candidate's `customer_id` so the reply is unambiguous to
  parse — no need to re-run the deterministic text matching that
  exists today for the button case, since the id *is* the answer.
- 4+ candidates → list message instead. This should be rare (decisions
  #8 mostly prevents it going forward) but must not break when it
  happens.
- Keep the existing text-reply matching path working too — a trader
  might type "1" instead of tapping, or be on an older client that
  doesn't render interactive messages. Buttons are an enhancement,
  not a replacement for the fallback.

**2. Low-confidence confirmation** (spec §23, already built as a text
prompt) — convert to three reply buttons: "Confirm" / "Edit" /
"Cancel". "Edit" can just prompt the trader to resend the message
correctly rather than building inline editing — that's real scope,
skip it.

**3. Greeting/help menu** — a bare greeting ("hi", "hello", "good
morning", equivalents across all five supported languages) should
reliably map to a friendly reply plus this quick-action menu, not fall
through to the financial-intent extractor and risk it force-fitting a
greeting onto CREATE_DEBT or another intent it doesn't belong to. This
is a real trader's most likely first message, not an edge case — treat
it as required, not optional polish.

Two ways to get there, pick whichever fits the existing intent schema
better without more surgery than needed:
- Add a `GREETING` value to the intent enum in `internal/ai`, handled
  the same way `HELP` is (no financial-service call, straight to a
  fixed reply), or
- Short-circuit before the AI call entirely for a small set of
  common greeting patterns per language, skipping the model call
  outright for the cheapest, fastest path — reasonable given how
  narrow and low-stakes this message shape is.
Either way: the reply should introduce Ruby briefly and offer the
buttons ("Record a debt", "Who owes me?", "Help") — don't just say
"Hello" back with nothing else, that wastes the trader's next message
on "what can you do."

## What NOT to build

- Product/catalog messages, CTA URL buttons, location requests — none
  of these apply to Ruby.
- Inline editing via buttons for the "Edit" confirmation option — text
  resend is enough.
- Multilingual button labels beyond what's needed — button titles are
  short and generic enough ("Confirm"/"Cancel", a name, a number) that
  they should already work across the five supported languages without
  needing separate localization logic; verify this holds rather than
  building a translation table preemptively.

## Testing

- A test proving inbound `button_reply`/`list_reply` webhook events
  resolve to the same outcome as typing the equivalent text reply
  today (parity, not a new code path with new bugs).
- A test confirming disambiguation correctly switches between
  buttons (≤3) and list (4+) based on candidate count.
- A test confirming button title truncation/length is respected before
  sending (don't let a long customer name blow past 20 characters
  unnoticed).
