# Brief — critical fixes, full reminder system, and interactive slot-filling

Fix in the priority order below. Don't parallelize the critical tier
with the polish tier. Test and confirm each tier before moving to the
next.

## Tier 1 — financial data integrity (fix first)

### 1a. Allow debt creation with no due date
Debt creation must succeed when no due date is given — never invent
one, never fail. Per spec §21, a due-date-less debt is a normal, valid
record. Find and fix wherever the current write path treats an
absent/nil due date as an error rather than a legitimate optional
field.

### 1b. Make customer resolution and debt creation atomic
Wrap customer lookup/creation and debt creation in a single
transaction, so a failure anywhere in that sequence rolls back
cleanly with zero partial state — no orphaned customer row left behind
if the debt write fails. This mirrors the same atomicity already
correctly enforced in the payment service; extend the same guarantee
to the AI-driven debt-creation path if it doesn't already have it.

Then: audit the current dev/demo database for any customer rows with
no associated debts (orphaned by the pre-fix version of this bug) and
clean them up — merge or remove, don't leave known-bad data in place.

Add a regression test: simulate a failure partway through debt
creation and confirm (a) no orphaned customer row survives, and (b) a
subsequent identical request doesn't create a duplicate customer.

### 1c. Decline unsupported requests honestly instead of improvising a flow
Any request that doesn't map to a real, supported intent must get an
explicit, honest decline — "I can't do that yet — here's what I can
help with: [capability list]." Never let the model improvise a
multi-turn flow for something that isn't a real feature (e.g.
invoicing). This needs to be a structural guard on the intent-handling
side, not left to model judgment per-request.

## Tier 2 — core capability fixes

### 2a. Wire LIST_CUSTOMERS to real data
Return the trader's actual customer list from `customer.Service`, with
a genuine empty-state message if there are none — not a stub response
with no content.

### 2b. Make language sticky per conversation
Once a language is established for a conversation, don't re-detect
fresh on every message. Only switch language on a message with enough
content to genuinely signal a change — never flip on a single short,
ambiguous word or name.

### 2c. Fix pronoun/context resolution
Confirm and fix why a pronoun immediately following a named customer
(spec §8 signal 4) isn't resolving via the existing Redis
conversational-context store. Add a direct test: name a customer, then
reference them by pronoun in the next message, confirm resolution.

## Tier 3 — response quality

### 3a. Split small talk, real questions, and true fallback into distinct
### paths
Replace the single generic capability-list fallback with three
distinct behaviors: a brief, warm reply for genuine small talk; a real
answer for questions Ruby should be able to answer (3b); and the
honest decline from 1c only for genuinely unsupported requests. Stop
using the capability-list block as a catch-all for all three.

### 3b. Answer self-referential questions from stored data
"What is my name?" and equivalents should read from `users.name`
directly and answer, not fall through to the generic response.

## Interactive slot-filling — ask for missing required info instead of
## failing or guessing

This is the core interactivity gap: right now, a message missing a
required field either fails outright or gets mishandled. Build a
general mechanism, not a one-off fix per intent, using the same
Redis-backed pending-state infrastructure already built for
confirmation/disambiguation/onboarding — this is a new trigger
condition and flow for that existing system, not new infrastructure.

**Required fields per intent** (this is the source of truth for what
triggers a follow-up question):
- `CREATE_DEBT`: customer identity and amount are required. Due date,
  description are optional — never ask for these, per 1a.
- `RECORD_PAYMENT`: customer identity and amount are required.

**Core behavior**: when a message is missing a required field for its
intent, don't fail and don't guess — ask specifically for the missing
piece, store the partial record in pending state, and complete it once
the reply arrives. Only call the actual backend service (debt/payment
creation) once every required field is present and validated — the AI
boundary rule stays intact, this is purely about *collecting* the
input before validation, not changing what gets validated or who
validates it.

**Edge cases to handle explicitly — this is the actual point of this
section, not a nice-to-have list:**

1. **Multiple missing fields at once** (e.g. "someone took goods,
   pays Friday" — no name, no amount). Ask one question at a time, in
   a sensible order (identity before amount), not a multi-part
   question crammed into one message.

2. **The reply fills the missing field AND adds something new** (e.g.
   asked for amount, trader replies "5k, also who owes me overall").
   Fill the pending slot and still handle the second request — don't
   drop it.

3. **The reply doesn't actually answer the question** (asked for an
   amount, got a name instead). Don't accept it blindly — re-ask,
   using the same "obviously doesn't match what was asked" pattern
   already built for the name-capture flow, generalized to apply to
   whichever field is actually pending.

4. **The trader wants out** — "never mind," "cancel," "forget it," and
   equivalents across all 5 languages must cleanly exit any pending
   slot-fill state, not just the name-capture flow specifically. This
   should be a general pending-state capability, not implemented
   per-flow.

5. **A genuinely unrelated message arrives while a question is
   outstanding.** If the reply doesn't look like an answer to the
   pending question AND reads as a complete, different request, handle
   that new request on its own terms, then either re-ask the original
   pending question afterward or let it lapse via the existing pending-
   state TTL — don't force a mismatched interpretation onto an
   unrelated message just because something was pending.

6. **Correcting a value already given, before the record is
   finalized** ("actually make that 7k, not 5k") — should update the
   pending record in place rather than requiring the trader to start
   over or being misread as a new, separate debt.

7. **Multiple transactions described in one message** ("Chinedu took
   5k and Ngozi took 3k") — explicitly out of scope for this pass.
   Don't attempt to half-build multi-entity extraction; if this comes
   up, decline gracefully per 1c rather than partially handling it.

8. **All of the above must work across all 5 supported languages**,
   not just English — this is a real requirement, not a stretch goal,
   consistent with everything else built so far.

## Full reminder system

**Trader reminders — automatic, no opt-in.** Schedule automatically
whenever a debt is created with a due date; this is the trader's own
data reflected back to them, no separate consent needed.

**Customer reminders — opt-in**, per the existing flow already built:
ask after debt creation, collect a phone number if missing.

**Scheduling and dispatch.** One reminder the day before `due_date`,
one on `due_date`. Implement an actual background worker that checks
for due reminders on a reasonable interval and sends them, updating
status through spec §20's state machine (SCHEDULED → PROCESSING →
SENT/FAILED), recording failure reason and attempt count on failure.

**The WhatsApp template constraint applies to both trader and
customer reminders** — any business-initiated message outside an
active 24-hour session window needs a Meta-approved template. This is
already correctly scaffolded from the prior session's work (the
templated-send API shape, the placeholder template name env var) —
extend it to the automatic trader-reminder path too, don't build that
one as freeform text.

## Reporting back

For Tier 1, confirm explicitly whether fixing 1a alone resolves 1b, or
whether 1b needed its own separate fix. For the slot-filling section,
walk through each of the 8 edge cases with either a passing test or a
direct explanation of how it's handled — this section is being graded
on the edge cases actually working, not just the happy path.
