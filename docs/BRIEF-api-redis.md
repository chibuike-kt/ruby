# Brief — API layer + Redis

Builds on Slice 1 (customer/debt/payment/ledger, already verified —
21 tests passing under -race). This adds an HTTP surface over those
services and wires Redis for three specific jobs. Not a rewrite of
anything in Slice 1 — call the existing services, don't duplicate
their logic in handlers.

## HTTP API (spec §43)

Router: `internal/httpserver`, chi. Wire real handlers for:

- `POST   /api/customers`        — customer.Create
- `GET    /api/customers`        — list, scoped to the authenticated user
- `GET    /api/customers/{id}`   — customer.GetByID
- `POST   /api/debts`            — debt.Create
- `GET    /api/debts`            — list, scoped to the authenticated user
- `POST   /api/debts/{id}/payments` — payment.Record (idempotency key
  from the `Idempotency-Key` header, required — 400 if missing)
- `GET    /api/ledger`           — ledger.ListByUser / ListByDebt
- `GET    /api/summary`          — total outstanding/collected/issued,
  derive from existing queries, don't add new service methods unless
  nothing already computes this

Skip for now (Slice 3 territory, don't build stubs for these):
`/api/reminders`, `/api/export`.


Every handler resolves `user_id` from request context, never a path
or body param — cross-user access must be structurally impossible at
the handler layer, matching what Slice 1 already enforces at the
service layer.

**Auth, for now**: no JWT yet — that's real scope, don't build it
under time pressure. Use a `X-User-ID` header read by a small
middleware that resolves it into a `users` row, 401 if missing/unknown.
Mark this clearly as temporary in a comment on the middleware itself
(one line, not a paragraph) so it isn't mistaken for the real auth
layer later.

Standard middleware stack: RequestID, Recoverer, structured request
logging (method/path/status/latency/user_id — never log amounts,
phone numbers, or customer names per spec §36), the rate limiter
below.

## Redis — three jobs, in this priority order

**1. Rate limiting (spec §35)** — token bucket or sliding window,
keyed by `user_id`, applied to the two mutation endpoints (`POST
/api/debts`, `POST /api/debts/{id}/payments`). A reasonable default
(e.g. 30 req/min) is fine; make it configurable via env, don't
hardcode.

**2. Webhook dedup fast-path** — `SETNX ruby:wh:{provider_message_id}`
with a short TTL (a few minutes) as a cheap pre-check before hitting
Postgres. This is an optimization only — the `messages` table's
unique index on `provider_message_id` remains the actual source of
truth for correctness (decisions.md #2). If Redis is down or the key
expired, Postgres still catches the duplicate; don't let a Redis miss
become a double-processed event. No WhatsApp webhook exists yet
(that's Slice 2) — just build the reusable dedup-check function now
so Slice 2 can call it directly.

**3. Conversational context store** — `ruby:ctx:{user_id}:last_customer`
→ customer_id, TTL ~10 minutes, set whenever a debt or payment is
recorded for a customer. This is what will let Slice 2 resolve "he
paid me 30k" to the right customer per spec §8 signal 4. Build the
get/set functions now in a small `internal/context` (or fold into
`internal/customer` if that reads more naturally) — Slice 2 wires it
into the AI resolution flow, not this slice.

Redis client: `internal/db` already has the Postgres pool pattern —
match that shape for the Redis client (single package-level
connection helper, not one client per caller).

## Testing

- Handler tests: table-driven, hit the real chi router with
  `httptest`, assert status codes and response bodies — not just "it
  compiles."
- Rate limiter: a test that fires N+1 requests and confirms the last
  one is rejected.
- Dedup cache: a test confirming a second call with the same
  provider_message_id is caught by Redis without touching Postgres,
  and a separate test confirming Postgres still catches it if the
  Redis key is deliberately absent (simulating a Redis miss).
- Context store: set then get, and TTL expiry (can use a short TTL
  override in the test rather than waiting 10 real minutes).

## Out of scope here

JWT auth, WhatsApp webhook itself, AI intent extraction, reminders,
export. Don't drift into these — flag if you think something here
requires touching them.
