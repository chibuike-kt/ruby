# Brief — final demo-critical fixes only

This is the last pass before the demo. Scope is deliberately narrow, only what a viewer would actually see go wrong on camera, found in a real live transcript against the deployed instance. Be conservative, this close to presenting, a new bug introduced while fixing these would be worse than leaving a minor one alone. Verify each fix against the exact transcript line it came from before moving to the next.

## 1. Slot-filling silently attaches a completed record to the wrong customer

Real sequence: "Someone took goods" → Ruby asks "How much?" → user replies "3k", **naming no one** → Ruby records the debt for "Emma," a customer from several messages earlier, never mentioned in this exchange at all.

The slot-filling flow is only prompting for the missing amount when identity is also unknown, and silently falling back to a stale last-referenced customer instead of asking. This is a real financial-attribution bug, not cosmetic, fix the field ordering so identity is always resolved (asked for explicitly if unknown, never assumed from stale context) before amount, matching what was originally specified. "Someone took goods" with no name given anywhere should ask who it's for first, the same behavior already working correctly earlier in the same transcript ("Someone took goods" → "Emma" → "How much for Emma?").

## 2. Confirmation prompt says "payment" but records a "debt"

"Someone took goods" → "20k" → Ruby asks to confirm a "*₦20,000* **payment**," Confirm/Edit/Cancel → confirming produces "**Debt** recorded." The confirmation screen and the result contradict each other on the one thing that matters most, what kind of financial event is actually happening. Fix the confirmation copy to match the actual intent being executed, if this is a debt creation flow, the confirmation must say debt, never payment.

## 3. Reminder phone number rejected and accepted in the same breath

A valid number ("08060514292") triggered "I just need a phone number... what should I use?" immediately followed, with no new input from the user, by "Got it — I'll remind Chinedu..." Find why the validation path is producing both an error and success response to the same single input, and fix so a valid number is accepted once, cleanly, with no contradictory message shown first.

## 4. "Who owes me?" doesn't group multiple debts per customer

The list currently shows a customer's name as a separate, disconnected entry once per debt, Emmanuel appeared three times, Emma twice, non-adjacent, no subtotal. Now that multiple debts per customer is clearly a normal case, group by customer: one entry per customer, each debt listed under it, with a per-customer subtotal before the grand total. This is directly visible in the demo the moment "who owes me" is asked with more than one debt on file, worth getting right.

## 5. Customer statement shows the literal word "item" instead of omitting it

When no description was given for a debt, the statement prints "*item*" as a line label rather than leaving it out gracefully. Compare against a debt that does have a description, which renders correctly. Fix so a missing description just doesn't render that piece, not a placeholder word.

## Explicitly not in scope for this pass

The inconsistency between when identity-confirmation triggers for a payment versus a debt creation, on a single existing match, is real but subtle and not obviously camera-visible, leave it alone tonight. The name-corruption seen in this session's testing looks operational (a mid-conversation redeploy interacting with a webhook retry), not a code bug, don't chase it, just avoid deploying while a real conversation is in progress from here on.

## Verification

For each of the five, reproduce the exact transcript sequence that surfaced it and confirm the fix directly against that sequence, not just a fresh isolated test. Run the full local check before pushing. Given the time left, this needs to be right the first time, don't rush past actually confirming each one.
