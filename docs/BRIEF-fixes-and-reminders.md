# Brief — name extraction fix, voice notes, identity confirmation, reminder opt-in

Four items: two real bugs, two new (but tightly scoped) features. Fix
the bugs first — they're actively broken in a live demo path.

## 1. Name extraction bug (blocking)

The name-capture flow (decisions/BRIEF-response-quality.md) currently
treats the trader's entire reply as their name — "I'm Kingsley" gets
stored as the literal name, not "Kingsley."

Fix: don't store the raw pending-name reply verbatim. Extract the
actual name from common self-introduction patterns before storing —
"I'm X", "I am X", "My name is X", "Call me X", "This is X", and
equivalents across the 5 supported languages, plus the bare case
(trader just replies "Kingsley" with nothing else, which should pass
through unchanged).

Two ways to implement, pick whichever fits the existing pipeline with
less new surface area:
- A lightweight extraction step reusing the same AI call already made
  for this reply (add a `extracted_name` field to whatever schema
  processes the pending-name state), or
- A small set of regex/prefix-stripping patterns per language if that
  keeps latency and cost lower for something this simple — reasonable
  either way, use your judgment on which fits the codebase better.

Whichever approach: don't weaken the existing "obviously not a
name" rejection check from the original flow — this fix is about
*extracting* the name correctly from a valid name-shaped reply, not
about loosening what counts as a valid reply in the first place.

Test explicitly: "I'm Kingsley" → stores "Kingsley". "My name is
Kingsley" → stores "Kingsley". Bare "Kingsley" → stores "Kingsley"
(regression — must still work). Equivalent phrasing in at least one
non-English supported language.

## 2. Voice notes failing (blocking)

Every voice note currently returns the generic failure message
("Something went wrong while recording that... please try again")
instead of processing. Given the generic-error-text pattern was built
deliberately to avoid leaking internals (spec §36/§37), this is
currently undiagnosable without added logging — same class of problem
solved earlier for HTTP 500s.

Fix in this order:
1. Add error-level logging (the actual Go error) to the voice-note
   pipeline specifically — the download → transcode → transcribe →
   extract chain — if it isn't already covered by the general error
   logging middleware. Reproduce a real failure and capture the actual
   error before guessing at a fix.
2. Likely candidates worth checking directly, since these are the
   points most likely to break silently:
   - Is `ffmpeg` actually present and on PATH inside the Docker
     container (Dockerfile was supposed to install it — confirm it's
     actually there at runtime, not just in the Dockerfile source)?
   - Is the WhatsApp Media API download step succeeding (media ID
     resolution → actual binary download — two separate API calls in
     Meta's flow, confirm both happen correctly)?
   - Is the transcoded file actually reaching `gpt-transcribe` in a
     format/size it accepts?
3. Fix the actual root cause once identified — don't paper over the
   symptom with a broader try/catch.
4. Add a regression test for whichever step was actually broken.

Report the actual root cause found, not just "fixed" — same standard
as every other bug this session.

## 3. Identity confirmation on name match (new)

Current behavior (decisions.md #8) only guards against creating a
*duplicate* customer record without a distinguishing signal. It
doesn't yet handle the adjacent case: a trader mentions a name that
matches an *existing* customer, but there's no way to know whether
they mean that same person or a different person who happens to share
a name — silently assuming it's the same customer is exactly the kind
of guess spec §11 forbids, and silently creating a duplicate isn't
right either if it really is the same person.

Fix: when `CREATE_DEBT` (or `RECORD_PAYMENT`) names a customer whose
name matches an existing customer for this trader, and no other
signal (phone number, alias, recent conversational context per §8
signal 4) disambiguates it, don't proceed either direction — ask:

> "You already have a customer named Chinedu. Is this the same
> Chinedu, or someone new?"

- **Same person** → resolve to the existing customer, proceed
  normally.
- **New person** → this is exactly decisions.md #8's creation guard —
  ask for a phone number or alias before creating the new record.

This uses the same pending-state mechanism already built for
confirmation/disambiguation — no new infrastructure, just a new
trigger condition for entering that flow. Add this as decision #9 in
decisions.md once implemented, describing the trigger condition
precisely (name match to an existing customer + no disambiguating
signal) so it's not confused with #8's narrower creation-time guard.

## 4. Reminder opt-in at creation time (new, scoped narrower than full reminders)

This is not the full reminder system from spec §18/19 — it's the
specific opt-in moment right after a debt is created, which is a
natural, low-effort place to start.

After a debt is successfully recorded and the trader gets their
confirmation, follow up (same message or immediately after) asking:

> "Want me to remind [customer name] before this is due?"

- **No** → nothing further, normal flow continues.
- **Yes**, and the customer has no phone number on file → ask for it
  now. Store it on the customer record.
- **Yes**, phone already on file → confirm and schedule.

Scheduling: one reminder the day before `due_date`, one on `due_date`
itself, sent as a WhatsApp message **to the customer's number**, not
the trader's.

**Read this before building the sending side — it's a real constraint,
not a formality.** WhatsApp's Business Platform requires a
Meta-approved message template for any business-initiated conversation
outside an active 24-hour customer service window. Since the customer
has never messaged Ruby, this is a business-initiated message from
message one — it cannot be a freeform text send, it must go through
an approved template. This means:

1. A template needs to be submitted to Meta for approval before this
   can actually send anything live — that approval process is not
   instant and is outside engineering's control. Build the scheduling
   and the reminder-record infrastructure now (the `reminders` table
   already exists per the original schema), but the actual send step
   should be implemented against the templated-message API shape from
   the start, not freeform text that would need rework later.
2. For the demo, if template approval hasn't come through in time,
   the honest fallback is: build and test the full pipeline up to the
   send call, and either mock/log what would be sent, or send to a
   pre-approved test template if one can be gotten approved in time.
   Don't build something that looks like it sends real reminders if
   it can't actually, and don't quietly skip this constraint — flag it
   back to me if approval timing becomes a real blocker.
3. The trader's "yes" to receiving reminders is consent on the
   trader's side; whether the *customer* has separately opted in to
   receiving business messages is Meta's own policy layer (opted-in
   phone numbers, template categories, etc.) — worth being aware this
   exists rather than assuming trader consent alone is sufficient,
   but the mechanics of Meta's customer opt-in system are out of scope
   to solve here; the template requirement above is the actual
   engineering-relevant constraint.

## Testing summary

- Name extraction: the four cases listed in section 1.
- Voice notes: whatever the actual root cause turns out to be, plus a
  regression test.
- Identity confirmation: name-match-to-existing-customer triggers the
  same/new prompt; "same" resolves correctly; "new" enters the
  existing decision #8 flow.
- Reminder opt-in: yes/no branches, phone-collection-if-missing branch,
  and a test confirming the actual send attempt uses the templated
  message shape, not freeform text (even if the template itself isn't
  approved yet in this environment).
