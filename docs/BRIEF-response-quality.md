# Brief — auto-onboarding + response quality pass

Two things bundled because they solve each other: fixing onboarding
gives the system a reliable signal for "is this a new or returning
trader," which is exactly what's needed to make greetings sound
natural instead of repeating a pitch every time.

## 1. Auto-create user on first contact, and ask for their name (required, blocking)

Right now an unknown sender's message is stored with
`processing_status = UNKNOWN_USER` and nothing happens — the user has
to be manually inserted into `users` first. Nothing in the spec
requires pre-registration, and it directly contradicts the product's
whole premise of zero-friction onboarding.

Fix: when a message arrives from a phone number with no matching
`users` row, don't guess a name from the WhatsApp profile — ask,
combined into the same message as the existing intro, not a separate
follow-up.

Context worth knowing: the trader's very first message is almost
always going to be exactly "Hi Ruby" — that's the fixed text sent by
the click-to-chat link/QR code this product is distributed through.
Don't over-engineer for varied first-contact phrasing; the greeting
path (section 2's first-contact case, effectively merged with this
flow) is the overwhelmingly common entry point. Still handle a
first-contact message with real content gracefully per step 3 below,
but it won't be the common case.

1. Create the `users` row immediately (so the phone number is claimed
   and idempotency/dedup still works normally), with `name` left
   empty/null.
2. Reply with **one combined message**: the existing intro/capability
   blurb (what Ruby can do) plus the name question, in the same
   message — not the intro first and a separate "what should I call
   you" as a follow-up turn. Something like: "Hi! I'm Ruby, your
   bookkeeping assistant. I can record a debt, tell you who owes you,
   or explain what I can do. First — what should I call you?" The
   existing 3 quick-action buttons (Record a debt / Who owes me? /
   Help) stay attached to this same message. Set a pending state for
   this user (same Redis-backed mechanism as confirmation/
   disambiguation) keyed to "awaiting name," carrying whatever their
   original message was verbatim.
3. If their original first message was more than a bare greeting (e.g.
   already a debt-creation message), don't discard it — it's stored
   in the pending state precisely so it can be processed right after
   the name is captured. A trader whose first message is a real
   request shouldn't have to repeat themselves.
4. If the trader taps one of the 3 quick-action buttons instead of
   replying with a name, treat that the same as a non-name text reply
   (see step 5) — it's not a name, but it *is* a clear, valid intent.
   Store the button's underlying intent as the pending original
   message (replacing whatever was there before, since a deliberate
   button tap is a stronger signal than whatever their first raw
   message was) and apply the same re-ask-once-then-fallback logic
   from step 5 for the name itself.
5. Their next message is treated as the name — but not blindly. If it
   obviously isn't a name (contains a currency amount, digits in a
   pattern that looks like a debt/payment message, or otherwise
   matches the shape of a real financial request rather than a
   short name), don't accept it as the name. Re-ask once, more
   directly ("I just need your name so I know what to call you —
   what should I put?"), and keep whatever they originally sent
   pending as before. If the *second* reply also doesn't look like a
   name, stop trying — save a generic placeholder ("Trader") as the
   name so the flow doesn't loop forever, and proceed normally
   (processing the original pending message if there was real content
   in it). A trader can always tell Ruby to call them something else
   later; don't block indefinitely on this.
6. Once a name is accepted — whether from the first reply or after
   the one re-ask — save it to `users.name`, clear the pending state,
   send a brief acknowledgment ("Nice to meet you, {name}!"), and if
   the original first message had real content beyond a greeting,
   process it now through the normal pipeline. Same applies to the
   placeholder-fallback path above: acknowledge, then process the
   pending message if there was one.
7. Store the name verbatim, reasonable length cap, no further
   validation gymnastics beyond the not-obviously-not-a-name check
   above — a trader's name is whatever they say it is.
8. This creation/name-capture moment is also the signal for section 2
   below — a phone number that just went through this flow is, by
   definition, a first contact. Once `users.name` is set, never ask
   again; use the stored name in the "welcome back" phrasing in
   section 2 (e.g. "Hello {name}! ...") rather than a generic greeting.

## 2. Greeting responses shouldn't sound like onboarding every time


Currently every greeting gets the same "introduce Ruby + show the
3-button menu" response regardless of whether this is the trader's
first message ever or their fiftieth. That's wrong — it should feel
like talking to an assistant that knows you, not a fresh pitch on
every "hi."

- **First contact**: handled by the name-capture flow in section 1 —
  don't duplicate that logic here, this is about every message after
  the name is known.
- **Returning trader** (an existing user with a stored name): a short,
  warm acknowledgment using their name instead — something like
  "Hello {name}! How can I help with your credit sales today?" — no
  re-introduction, no re-explaining what Ruby is. Buttons are optional
  here; if included, keep them but don't lead with the pitch.

## 3. HELP intent needs a real, useful response

Right now HELP-shaped messages don't reliably produce something
useful. Fix: a clear, concrete explanation of what a trader can
actually do — recording a debt, recording a payment, checking who
owes them, checking a specific customer's balance — phrased as
examples of things to say, not a feature list. E.g., show 2-3 example
phrasings a trader could send, not just intent names.

## 4. LIST_OUTSTANDING_DEBTS needs real formatting, not a flat dump

"Who owes me?" should return a genuinely readable list, not a wall of
text. Use WhatsApp's supported lightweight formatting
(`*bold*`, line breaks, `-` for bullet-style lines) to make it
scannable — customer name bolded, amount and due date on the same or
next line, one entry per line, blank line between entries if it aids
readability. Total outstanding across all customers as a closing
line if there's more than one debtor.

**Empty state matters as much as the populated case**: if there are no
outstanding debts, say so plainly and warmly — not silence, not an
error, not an empty list rendered awkwardly. Something like "You're
all clear — no one owes you right now."

## 5. General phrasing/formatting pass across every response

The existing responses (confirmations, errors, disambiguation prompts)
work correctly but read flat — apply WhatsApp's formatting consistently
(`*bold*` for amounts and customer names, line breaks to separate
distinct pieces of information rather than run-on sentences) and a
warmer, more professional tone throughout. This applies to the
phrasing system prompt/instructions broadly, not just the two intents
called out above — go through the existing DEBT_CREATED,
PAYMENT_RECORDED, and confirmation/error templates and apply the same
formatting standard so the whole product feels consistent, not just
the two flows mentioned by name here.

**Important — this must not weaken the grounding backstop
(`isGrounded`) already built.** Formatting instructions go in the
system prompt/style guidance; they must not give the model any more
latitude to state a figure that isn't in its input. If applying
formatting requires restructuring `PhraseInput` or the prompt, keep
the same discipline: every number in the output must still trace to
an actual field. Re-run the grounding tests after this change and
confirm they still pass unmodified — if they need touching to pass,
stop and treat that as a signal the change is unsafe, not something
to patch around.

## Testing

- New: message from a never-before-seen phone number creates a user
  and gets asked for their name, not stuck at UNKNOWN_USER.
- New: replying with a name saves it, acknowledges warmly by name,
  and clears the pending state.
- New: replying with something that looks like a real request instead
  of a name (e.g. contains a currency amount) triggers exactly one
  re-ask, not immediate acceptance.
- New: tapping a quick-action button while a name is still pending is
  treated the same as a non-name reply — triggers the re-ask, and the
  button's intent is processed once the name flow resolves.
- New: a second non-name-looking reply after the re-ask falls back to
  a placeholder name and proceeds, rather than looping indefinitely.
- New: if the original first message had real content beyond a
  greeting (e.g. a debt-creation request), it's processed
  automatically right after the name is captured — not lost, not
  requiring the trader to resend it.
- New: a returning trader with a stored name gets greeted by name,
  never asked for their name again.
- New: message from a phone number with existing activity gets the
  short returning-trader greeting, not the full intro.
- New: HELP produces a response containing concrete example phrasings,
  not just a generic message.
- New: LIST_OUTSTANDING_DEBTS with zero debts produces the empty-state
  message, not an empty/malformed list.
- New: LIST_OUTSTANDING_DEBTS with multiple debts produces one entry
  per debtor with formatting applied.
- Regression: all existing `isGrounded` tests still pass with zero
  modification to their assertions.
