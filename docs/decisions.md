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
