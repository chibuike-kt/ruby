# Ruby — instructions for Claude Code

## Hard rules — never violate these

- **Never run any git operation.** No `git commit`, `git push`, `git add`,
  `git checkout`, `git branch`, `git merge`, no staging, no committing,
  nothing. Kingsley reviews and commits everything himself. If a task
  seems to require a commit, stop and hand back control instead.
- **No unnecessary comments.** Comment only where the *why* isn't obvious
  from the code itself (a non-obvious constraint, a spec section being
  satisfied, a deliberate deviation from the "obvious" approach). Never
  narrate what a line does when the code already says it. No comment
  headers, no restating function names in prose above them.
- **No AI-sounding boilerplate.** No "Certainly!", no filler docstrings,
  no padding a function with a comment just to look thorough.
- Follow the existing package boundaries in `internal/` — one concern
  per package (customer, debt, payment, ledger, reminder, whatsapp, ai,
  account, export). Don't collapse them into a god-package for
  convenience.

## Project

Ruby: WhatsApp-native financial assistant for informal traders. Full
spec is in `docs/spec.md` (paste the original brief there if it isn't
already). Stack: Go 1.26.4, Postgres 18.4, Redis 8.10.0, Docker.

Core architectural rule: **WhatsApp is the interface, Postgres is the
source of truth.** AI interprets language into a structured intent;
the backend validates and mutates; the ledger is append-only.

## Non-negotiable invariants

- All money is `internal/money.Money` — integer minor units, never
  float64, anywhere in the codebase.
- Every inbound WhatsApp event is deduped on `provider_message_id`
  before any processing (see `messages` table unique index).
- Every payment write requires an idempotency key, unique in `payments`.
- Concurrent payments against the same debt use `SELECT ... FOR UPDATE`
  inside a single DB transaction — never optimistic retry loops for
  this path.
- `ledger_entries` is insert-only. Never generate an UPDATE or DELETE
  against it.
- AI output (`internal/ai`) never reaches a service or the DB directly —
  it must pass through a validated DTO first.
- Debt status transitions follow the state machine in the spec
  (`OUTSTANDING → PARTIALLY_PAID → SETTLED`); reject invalid transitions
  at the service layer, not just in tests.

## Workflow

- Work in small, reviewable chunks. Finish one vertical slice (e.g.
  "debt creation end-to-end with tests") before starting the next.
- Run `make vet lint test-race` before considering anything done.
- Leave the working tree dirty for Kingsley to review and commit —
  do not stage or commit it yourself, per the hard rule above.
