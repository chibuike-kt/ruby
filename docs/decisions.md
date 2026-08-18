# Decisions

These resolve ambiguities in docs/spec.md. Treat them as settled, not
open — they've already been reasoned through, don't relitigate unless
something concrete contradicts them during implementation.

1. **Money type** — `internal/money.Money`, integer minor units, no float
   anywhere. Already implemented with tests.
2. **Idempotency scope** — one row per `(user_id, provider_message_id)`
   in `messages`. A resend with edited content after a transcription
   retry is a deliberate reprocessing decision the service layer makes
   explicitly, never automatic.
3. **Concurrency (spec §33/§34)** — `SELECT ... FOR UPDATE` on the debt
   row inside the payment transaction. Not optimistic locking.
4. **Ledger immutability (§16)** — enforced at the DB level: the app's
   Postgres role gets no UPDATE/DELETE grant on `ledger_entries`, not
   just app-level discipline.
5. **AI boundary (§10/§42)** — `internal/ai` produces a typed
   intermediate struct. A separate validator turns that into a DTO the
   service layer accepts. No path from raw AI output to a service call.
6. **Overpayment (§14)** — the ambiguity prompt's default answer is
   "record the actual outstanding amount," never "record what they
   said." Recording the excess as a separate transaction requires an
   explicit distinct intent, not a fallback interpretation of "yes."
7. **Out-of-order events (§40)** — enforced via the `debt_status` enum
   plus explicit transition validation in the debt service. Events that
   don't fit the current state get deferred/rejected, never force-applied.
8. **Duplicate-name customers with no distinguishing signal (extends §7/§8)**
   — prevent this at creation, don't just resolve it later. When
   `customer.Create` receives a name that already exists for this
   `user_id` and no `phone_number` or `alias` is supplied, reject with
   a prompt asking for one of the two before the record is created —
   don't silently create an indistinguishable duplicate. This is the
   real fix; everything below is fallback for when it happens anyway
   (an existing duplicate from before this rule, or the trader
   insisting on skipping it if that's ever allowed).
   When disambiguation is still needed at resolution time
   (`customer.Resolve`'s `AmbiguousError`), never use debt attributes
   (amount, due date) as a tiebreaker — two debts matching on amount
   and due date is coincidence, not identity, and treating it as a
   signal is exactly the kind of guess spec §11 forbids. Surface the
   debt's `description` field in the disambiguation prompt where it
   differs between candidates ("2 cartons of noodles" vs "1 bag of
   rice") — it's a real distinguishing signal that's easy to miss.
   True worst case — name, amount, due date, and description all
   identical, no phone/alias on either record: fall back to
   `customer_id` + `created_at`, since those exist unconditionally on
   every row and can't run out of distinguishing power. Prompt with
   creation order ("the one added Aug 2" vs "the one added Aug 15"),
   not raw IDs — a trader can't reason about an opaque ID but can
   usually recall roughly when they added someone.
   Once a trader resolves an ambiguity for a given name, cache that
   resolution in Redis for the session window (same store as the
   conversational-context cache from docs/BRIEF-api-redis.md) so an
   immediate follow-up message doesn't force them through the same
   disambiguation again.
