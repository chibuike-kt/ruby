# Brief — competitive positioning, systematic hardening, professional standard, innovation

Research-backed. Read the whole thing before starting, the sections build on each other.

## Part 1 — where Ruby actually stands, for context on everything below

**PayWise won the Nomba x DevCareer hackathon** ($4,000 grand prize, beat over 1,000 developers), built by a 17-year-old, same core idea as Ruby: WhatsApp voice notes recording informal traders' credit sales in English, Yoruba, Igbo, Hausa, with automatic reminders and balance tracking. This is real, judged external validation, a competition already rewarded this exact concept. Ruby's direction is right.

**Xara** (a much larger, funded, "went viral, landed the founder an xAI offer" WhatsApp banking bot) is worth knowing precisely, not just visually copying. As of its founder's own press interview, Xara had **only English and Pidgin** — Hausa and Yoruba were still "in progress." Ruby already has all five languages working now. That's a real, provable, honestly-stated edge, worth using directly, not just implying.

**The single most-cited real user concern about this entire product category is security and trust**, specifically, "the security of their financial and personal information," named explicitly in press coverage as something actively holding back adoption. This is exactly why the Security section built earlier matters, and it's worth going further, see Part 4.

Neither of these products is Ruby's ceiling, both are real signals about what actually resonates and what actually worries real users. Everything below is built on that, not on guessing.

## Part 2 — remaining unresolved items from earlier passes

**Identity confirmation trigger inconsistency (flagged earlier, deferred).** A bare name matching one existing customer triggered the same-or-new question for a payment, but not for several debt creations against an already-established single match. Audit whether this is intentional (e.g. debt creation trusts recent conversational context more readily than a payment does) or a genuine inconsistency, and make the behavior deliberate and documented either way, not accidental.

**Reminder phone number contradiction, needs fresh eyes.** Previously investigated exhaustively and not reproduced, a real, honest outcome, not a failure. Before dropping it, add structured logging specifically around the phone-validation path (log the raw input, the validation result, and which reply was actually queued, at the point of decision) so if it happens again, it's diagnosable in one look rather than requiring another full investigation from scratch.

## Part 3 — systematic hardening, grounded in real research on why conversational products fail

A few findings worth building around directly, not just as inspiration:

- **62% of users abandon a tool after a single bad encounter.** This means Ruby doesn't get a second chance to recover trust after visibly failing once, every one of the fixes below matters more than it might seem in isolation.
- **"People don't want to repeat themselves to machines"** (a repeatedly-cited finding in this space). Audit every slot-filling and re-ask path specifically for this, if a trader already gave a piece of information once, Ruby must never ask for it again in the same flow, even after a correction, cancellation, or an unrelated interruption.
- **Repetitive identical fallback messages are a named, specific failure mode.** If the same clarifying question needs to be re-asked, it should never be the exact same sentence twice in a row, vary the phrasing or narrow the ask (e.g. first "How much?", second time "Just the amount, like 5000 or 5k, is fine").
- **"Flow breakage," a process unexpectedly exiting or resetting due to a logic bug**, is a named failure category, this is exactly the class of bug chased earlier tonight (Edit resetting to onboarding). Whether or not that specific instance was ever real, treat this as a category worth systematically testing, not a one-off, write a test that drives every button (Confirm, Edit, Cancel, Yes, No, Same person, Someone new) from every flow that has one, and asserts none of them ever produce an onboarding-flow response for an already-named user. One test class covering the whole category is worth more than five isolated regression tests.
- **Confidence-based clarification, already correctly applied to voice transcription, extend the same principle everywhere an extraction is genuinely uncertain**, not just the cases already covered.

**One operational hardening item specific to tonight's deploy history**: confirm that Redis-backed pending state survives an API process restart correctly, and that a webhook delivery retried by Meta during a deploy can never be reprocessed as if it were a fresh reply to a still-open pending question. Add a test that simulates exactly this, a pending state exists, the process restarts, the same webhook message ID arrives again, confirm it's still correctly deduped and doesn't corrupt the pending flow. Given tonight's suspected redeploy-related name corruption, this is worth confirming directly rather than left as a theory.

## Part 4 — professional, enterprise-grade phrasing standard

Apply this checklist to every response in the codebase, not just new ones written from here on:

- No more than one exclamation mark in any single message, and only where it's genuinely warranted (a settled debt, a first greeting), never in routine confirmations or error messages.
- No filler openers ("Sure!", "Great question!", "Absolutely!"), start directly with the actual content.
- Currency always formatted identically everywhere it appears (already centralized in `money.FormatNaira`, audit for any response path that still formats a number manually instead of using it).
- Consistent capitalization of field labels across every response type (Amount, Outstanding, Due date, should never appear as "amount" lowercase in one place and "Amount" capitalized in another).
- Every error message follows the existing pattern from spec §37, specific, reassuring about data safety, never a raw technical detail, audit any newer response paths added this session for drift from this standard.
- A one-time trust-building message, wired as a real answer to "is my data safe" / "is this secure" / equivalents, not just a landing-page FAQ entry, directly addressing the top documented real-world concern for this whole product category. Keep it plain and factual: data isolation, the deterministic backend boundary, never state a security certification Ruby doesn't actually have.

## Part 5 — innovation, with real days available this changes the calculus

One more research finding worth knowing before this section: by 2026, **text, voice, and photo are described as the standard expected input trio** for this exact product category, WhatsApp finance assistants that let a user "log a transaction by text, voice note, or photo of a receipt" in one plain description found directly. Ruby has text and voice. Photo input isn't a stretch feature here, it's closing a real, expected gap. That reframes the priority order below.

Given the deadline is genuinely days away, not hours, build in this order, each tier assumes the one before it is solid first:

### Tier 1 — build these for real, not as stretch goals

**Photo input, and it should reopen the multi-transaction question properly.** A trader photographing a page of their existing paper notebook, or a single receipt, is table-stakes for this category now. Wire image messages through the same AI pipeline (the OpenAI models already in use accept image input directly, no new provider needed), extract however many transactions are actually visible, and run each one through the exact same validation/confirmation pipeline already built for text. This is the natural, well-scoped moment to properly build what Tier 4 declined earlier, a photo of a notebook page is inherently a multi-transaction input, this isn't optional if photo support is being added at all. Reuse the existing per-transaction confirmation flow for each extracted line, don't build a separate bulk-confirm flow, consistency matters more than speed here.

**A proactive periodic digest, sent without being asked.** Reuse the reminder dispatcher's existing scheduling infrastructure rather than building new infrastructure, a weekly summary (transactions recorded, amount collected, amount still outstanding, anything due soon) sent automatically. This is the single most differentiated pitch angle available, a tool that reaches the trader before they think to ask, not just one that answers when prompted.

**Voice replies from Ruby, not just voice input.** Genuinely rare in this category, nothing found in research shows a competitor doing this. Generate speech from the phrased response text (the same OpenAI account already in use for transcription has a TTS capability) and send it as a WhatsApp audio message, at minimum for voice-note-originated messages, so a trader who spoke to Ruby gets spoken back to, closing the loop naturally rather than switching modes on them. This is the kind of thing that makes a demo audience audibly react, prioritize it.

### Tier 2 — build if Tier 1 lands cleanly with time to spare

**A late-payment risk signal, tied directly to the Wema pitch.** When creating a new debt for an existing customer, if that customer's payment history shows a pattern (paid late more than once, for instance), surface it to the trader unprompted, "heads up, Emmanuel has paid late on 2 of his last 3 debts." This is cheap to compute from data that already exists (due dates versus actual payment timestamps in the ledger), and it directly reinforces the Credit Profile API story with a concrete, visible, in-product example of the same behavioral signal Wema would eventually see in aggregate.

**A minimal read-only web view of the ledger**, reinforcing "WhatsApp is the interface, Postgres is the record" visually, not just as a claim. This is a bigger, real lift, a new authenticated page hitting the existing REST endpoints, best coordinated with whoever's working the landing-page repo rather than built as backend-only work. Worth doing if there's genuinely a full extra day free, not worth starting if it would come at the cost of Tier 1.

### Still not worth pursuing, regardless of time available

Anything involving actual payment movement, a wallet, multi-currency support, or expanding outside the credit-sales domain. More time doesn't change this, these work against the deliberate, defensible scope that's made Ruby credible so far, they'd work against it even with weeks available, not just hours.


## Reporting back

Part 2 and Part 3's flow-breakage test class come first, they're the ones most likely to prevent something embarrassing on demo day, and they don't cost much time relative to their value. After that, Part 5's Tier 1 is where the real remaining time should go, photo input (which properly resolves the deferred multi-transaction question), the proactive digest, and voice replies are the three most likely things to make a judge or a viewer genuinely sit up. Part 4's phrasing audit can run in parallel with any of the above, it's not sequential-dependent on anything else here.
