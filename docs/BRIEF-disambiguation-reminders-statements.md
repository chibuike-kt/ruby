# Brief — critical regression, duplicate-name disambiguation, standalone reminders, statements, multi-transaction

## Tier 0 — critical, fix before anything else below

### Tapping "Edit" traps the entire conversation in a permanent onboarding loop

Real transcript: tapped Edit on an overpayment confirmation. Ruby responded with the full first-contact onboarding message ("Hi! I'm Ruby... what should I call you?") to an already-named, already-active user. Every message after that, including a completely unrelated "Help" and "Who owes me?", kept getting the same "I just need your name" response. The conversation never recovered.

This is the single most damaging bug possible in this product: once triggered, it makes Ruby completely unusable for that trader, with no way out visible to them. Fix this before touching anything else in this brief.

Find the actual root cause, don't guess: the leading hypothesis is that clearing the pending state on Edit is somehow being interpreted elsewhere as "no pending state = treat as first contact," or Edit's handler is incorrectly clearing or failing to read `users.name`. Reproduce the exact sequence (an overpayment triggering Confirm/Edit/Cancel, tap Edit) and trace what actually happens to the user's row and pending state. Add a regression test that specifically asserts a named, already-onboarded user's `users.name` and general capability are unaffected by tapping Edit on any confirmation flow, and that a subsequent unrelated message (like Help) is handled normally, not re-onboarded.

## Tier 1 — duplicate-name handling isn't actually firing

Two real gaps found in the same transcript, both regressions against decisions already made (#8, #9):

### 1a. Decision #8's creation-time alias guard didn't fire for a second same-named customer

A second "Emmanuel" was created (crocs, 50k) with no alias, no phone, while a first "Emmanuel" (pure water, 300) already existed, and Ruby never asked for a distinguishing signal at creation. Audit every path that can create a debt and, through it, a customer, text, voice, and the slot-filling flow specifically, and confirm all of them actually call the same duplicate-name check. It's likely this check only runs on one code path and the others were added later without wiring it in.

### 1b. Decision #9's same-or-new question didn't fire on a later ambiguous reference

With two Emmanuels on file, "Emmanuel has paid 10k" silently resolved to the first Emmanuel (₦300 outstanding) rather than asking which one, and rather than preferring the more recently mentioned Emmanuel per conversational context (spec §8 signal 4). Audit why the ambiguity check isn't triggering here and fix it so any bare name matching more than one existing customer always asks, never silently picks one, regardless of which direction (creation or reference) triggered the match.

### 1c. New requirement — alias-aware disambiguation that doesn't assume the trader remembers the alias

If a trader creates a customer as "Emmanuel mechanic" and later just says "Emmanuel" while a second, alias-less Emmanuel also exists, Ruby needs to ask which one, and the trader may genuinely not remember which alias they used. The disambiguation prompt should present real distinguishing detail for each candidate, not force the trader to recall their own past alias choice: name and alias if set ("Emmanuel (mechanic)" vs "Emmanuel"), phone number if on file, the most recent transaction's item description, or creation date as a last resort, matching the same fallback hierarchy already established for the true-worst-case scenario in earlier disambiguation work.

## Tier 2 — reminders need to work outside the creation-time opt-in moment

Two real gaps:

### 2a. `CREATE_REMINDER` needs to be a standalone, anytime intent

"Can you remind me about his payment tomorrow?" asked well after debt creation got "reminders aren't available yet," even though the reminder system is built and working for the opt-in-at-creation path. Wire `CREATE_REMINDER` as its own intent, invokable at any point in a conversation about any existing debt, not only immediately after creating one.

### 2b. Reminders shouldn't require a formal due date on the debt to exist

The debt this was asked about had no due date. A trader should still be able to say "remind me tomorrow" or "remind him Friday" and have that succeed, using the date given in the reminder request itself, independent of whether the underlying debt has a due date on record. If the reminder request itself doesn't include a date either, ask for one at that point rather than declining outright.

### 2c. Add `CANCEL_REMINDER`

Already in the original intent list (spec §9), appears never wired up. A trader should be able to cancel a scheduled reminder by referencing the customer or debt it's attached to.

## Tier 3 — real customer statements, not a decline

Earlier work correctly taught Ruby to decline undefined requests like "generate an invoice" rather than hallucinating a fake flow. That guard was right at the time, invoicing wasn't a real feature. It's time to make it a real feature instead of continuing to decline it, since the underlying data (every debt, payment, item description, and date per customer) already exists.

Add a new intent, something like `GET_CUSTOMER_STATEMENT`, recognized from natural phrasing, not only the literal word "invoice", "give me a breakdown for Emmanuel," "summarize what he owes," "show his account," and equivalents should all resolve to this intent. Format the response deterministically, the same reasoning already applied to `LIST_CUSTOMERS`: this is real financial data being read back to a trader, route it around the Phraser entirely rather than trusting a generated summary of numbers that already exist in the database. Include: every debt with item description and date, every payment against each, and the current outstanding balance. This is a formatted WhatsApp message, not a generated PDF or file, keep it scoped to that.

## Tier 4 — multiple transactions in one message (explicit scope reversal, largest lift in this brief)

An earlier brief scoped this out entirely as a non-goal, declining gracefully rather than half-building it. That was the right call at the time given everything else in flight. It's now being explicitly requested back in: "Chinedu took 5k and Ngozi took 3k" (by text or voice) should be recognized as two separate transactions and both recorded correctly, not declined.

This is a genuinely bigger architectural change than everything else in this brief, worth being honest about before starting: the intent extraction schema needs to support returning an array of proposed transactions from a single message instead of one, and the processor needs to loop through executing each independently, each still going through the exact same validation, slot-filling, and disambiguation logic as a single-transaction message. If one of the two transactions in a message is missing information or hits a name ambiguity, that specific one needs its own follow-up while the other, if complete, still proceeds normally, don't let one incomplete transaction block a complete one sitting next to it in the same message.

Given the size of this relative to Tiers 0 through 3, attempt it last, and only once those are solid and verified. If time runs out before this is reached, that's an acceptable outcome, a correct decline on a multi-transaction message is far better than a half-working attempt at parsing one.

## Additional edge cases worth building while in this area

- **Renaming or setting an alias on an existing customer after the fact** ("call him Emmanuel mechanic from now on"), a trader may want to add a distinguishing alias retroactively once they realize they have two same-named customers, not only at the moment a duplicate is first detected.
- **Validate the reminder phone number format** before accepting it, rather than storing whatever was typed unchecked.
- **Voice notes continue to work correctly through all of this**, confirmed already working in the transcript ("no one owes you" was a correct response to a voice-note query), don't regress this while making the changes above.

## Reporting back

Tier 0 is the one item in this brief that needs to be reported on with full confidence, including the actual root cause found, before moving to anything else. For Tiers 1 through 3, confirm each fix against the actual failing scenario from the transcript, not just a new isolated test. For Tier 4, report honestly on how far it got if time runs out partway, don't present a partially-working multi-transaction parser as complete.

# Brief — daily sales breakdown + demo polish additions

## Daily sales breakdown

A trader should be able to ask "what did I sell today," "show today's sales," "give me a breakdown for today," and equivalents, and get a professional, deterministic summary of everything recorded that day, server timezone, matching the existing `DEFAULT_TIMEZONE` config already used for due-date resolution.

New intent, something like `GET_DAILY_SUMMARY`. Format deterministically, same reasoning as `LIST_CUSTOMERS` and the customer statement work, this is real financial data being read back, route it around the Phraser entirely. Include:

- Every debt created today: customer, item description, amount, time.
- Every payment recorded today: customer, amount, time.
- Total credit issued today, total collected today.
- If any existing debt (not necessarily created today) has a due date of today or tomorrow, surface it as a short highlighted line at the end, something a trader would genuinely want flagged without having to ask separately. This is a real, useful addition, not filler, don't build a whole separate reminders-due digest around it, just the one line if applicable.

## Demo polish, kept small and low risk given the time left

These are worth doing specifically because they're cheap, given the typing-indicator and formatting work already in place, and because they add real, visible warmth to the product without new architectural risk. Skip anything that starts to feel like a bigger lift than it looks, that's not the intent here.

**Instant emoji reaction on receipt.** The moment a message is understood well enough to proceed (after signature verification, before the full response is ready), send a message reaction (Cloud API supports this directly, react to the inbound message's ID with an emoji, no template needed since it's not a business-initiated message). A simple ✅ or 👍. This layers naturally on top of the typing indicator work from the last brief, same point in the pipeline, and gives an extra beat of responsiveness that's genuinely rare to see in a hackathon demo.

**A small celebratory note when a debt is fully settled.** When a payment brings a debt's outstanding balance to exactly zero, the confirmation message should say something warmer than the standard payment-recorded template, a short, distinct line acknowledging the debt is fully paid off. This is a real, different financial event (spec's own `DEBT_SETTLED` ledger event already exists), it deserves to read differently, not just as another payment line.

## Testing

- Daily summary: correct totals and highlight line, correct empty state if nothing was recorded today, deterministic formatting confirmed (no Phraser call in the code path).
- Reaction: fires once per inbound message at the right point in the pipeline, doesn't fire for deduped messages, same discipline as the typing indicator's own test.
- Settlement message: a payment that brings outstanding to exactly zero produces the distinct settled message; a partial payment does not.
