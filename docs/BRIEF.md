# Brief for Claude Code — start here

40 hours to a working demo. Read `docs/spec.md` (full brief) and
`docs/decisions.md` (settled ambiguities) first, then `CLAUDE.md` for
hard operating rules — especially: never run git commands, minimal
comments.

## Already done, don't redo

- Directory skeleton under `internal/` — one package per service
  boundary from spec §42.
- `internal/money` — Money type, integer minor units, full test
  coverage. Use this everywhere money is touched.
- `go.mod` pinned to Go 1.26.4 and current dependency versions
  (pgx v5.10.0 — this floor matters, it fixes CVE-2026-33816 CVSS 9.8).
  Run `go mod tidy` first thing to generate `go.sum` for real — it
  wasn't generated here because this sandbox's network is restricted.
- `docker-compose.yml` — Postgres 18.4-alpine, Redis 8.10.0-alpine.
- `migrations/000001_init.up.sql` / `.down.sql` — full schema from
  spec §30 (users, customers, debts, payments, ledger_entries,
  reminders, messages), with the constraints spec calls out: positive
  amounts, `debt_status` enum, unique idempotency key on payments,
  unique provider_message_id on messages.
- CI (`.github/workflows/ci.yml`) — lint, vet, go.mod tidy drift check,
  migrate up, build, `-race` tests, against real Postgres/Redis service
  containers. This should go green on an empty-but-compiling repo; keep
  it green after every slice.
- `.golangci.yml`, `Makefile`, `.env.example`, `Dockerfile`.

## Immediate first steps

1. `go mod tidy` to generate a real `go.sum`.
2. Stand up `docker compose up -d`, `make migrate-up`, confirm CI's
   local equivalent (`make vet lint test-race`) passes on the empty
   scaffold before writing business logic.
3. Add the DB role grant restricting `ledger_entries` to INSERT-only
   for the app role, per decisions.md #4 — this is a follow-up
   migration, not part of `000001_init`.

## Build order (matches the spec's own 3-day scope, §44, compressed)

**Slice 1 — financial core, no WhatsApp, no AI.**
`internal/customer`, `internal/debt`, `internal/payment`,
`internal/ledger`. Get these fully correct and tested before touching
the WhatsApp layer at all — this is the part a judge can break, so
it's the part that has to be bulletproof:
- Debt creation, partial payment, full payment, overpayment protection
  (§14), duplicate-name resolution (§7/§8), the state machine (§38).
- The concurrency test from §34 exactly as specified: 10 concurrent
  ₦50,000 payment requests against a ₦50,000 debt → exactly 1 success,
  9 rejected, outstanding = 0. Write this test early; it's the one
  that proves the locking strategy actually works, not just compiles.
- Idempotent payment recording via unique `idempotency_key`.

**Slice 2 — WhatsApp + AI.**
`internal/whatsapp` (webhook, signature verification, dedup on
`provider_message_id`), `internal/ai` (intent extraction → typed DTO,
never touches DB directly per decisions.md #5), customer identity
resolution through the six-signal priority order in §8.

**Slice 3 — reminders + demo polish.**
`internal/reminder` (trader + customer reminders, retry/failure
states from §20), then the exact 7-step demo scenario in spec §46 —
build toward that scenario directly, it's the acceptance test.

## What to explicitly skip (§48 non-goals)

No wallet, no bank account, no lending, no payment gateway, no POS, no
full accounting software, no inventory, no marketplace, no credit
scoring, no full multilingual support (English-only is fine for the
demo), no KYC, no banking integrations, no automated lending. If a
task seems to be drifting into any of these, stop and flag it instead
of building it.

## Reporting back

At the end of each slice, summarize: what's implemented, what's
tested, what CI checks pass, and what's explicitly deferred. Don't
mark something "done" if the concurrency/idempotency tests for it
aren't written and passing — those are the actual bar per §45, not
just "the endpoint responds."
