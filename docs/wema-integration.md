# Ruby → Wema — the integration pathway

This is a pitch/architecture document, not a build spec. The goal is
to give judges a credible, honest answer to "how does this connect to
Wema," without overclaiming an integration that doesn't exist and
without violating spec's own non-goals (§48: no wallet, no banking
integration, no automated lending decisions — Ruby stays a bookkeeping
tool, never a payments product).

## The narrative (already implicit in the product, spec §47)

Most of Ruby's users — informal traders extending credit to known
customers — are exactly the population traditional banking underserves,
because banks have no data to assess them. They have real repayment
behavior, real transaction volume, real business relationships — it's
just never been captured anywhere a bank could see it.

Ruby is, by construction, building that data:

```
Informal credit sales (already happening, off the books)
       ↓
Ruby records it — structured, timestamped, verified by the ledger
       ↓
A trader's repayment history becomes provably real, not anecdotal
       ↓
With the trader's explicit consent, that history becomes a credit signal
       ↓
Wema can use it to extend working capital, faster and to more people
than a traditional credit check ever could
```

This isn't hypothetical — it's the same underlying idea as alternative
credit scoring already used by mobile money lenders across Africa
(transaction history as a credit signal where formal credit history
doesn't exist). Ruby's differentiator is that the data is *already*
verified by construction: it comes from an immutable ledger with
duplicate/overpayment/idempotency protection already built and proven,
not self-reported.

## What Ruby explicitly does NOT become

Worth stating clearly, since this boundary is what keeps the story
honest and keeps Ruby inside its actual scope:

- Ruby never moves money. No wallet, no disbursement, no repayment
  collection on Wema's behalf.
- Ruby never makes a lending decision. It exposes data; underwriting,
  risk, and the actual credit decision stay entirely with Wema.
- Ruby never shares data without the trader's explicit, revocable
  consent. This isn't a data-harvesting play — the trader owns their
  record, same principle as spec §26's account-recovery philosophy
  applied to a new context.

## The concrete artifact — a Credit Profile API

Rather than just describing the pathway in a slide, build a small,
real, working piece of it: a consent-gated, read-only endpoint that
returns a *summary*, never raw transaction detail, of a trader's
verified credit behavior. This is genuinely buildable in the time
available, and gives the demo something real to point at instead of
only a narrative.

```
GET /api/credit-profile/{user_id}
```

Requires:
- The trader's explicit opt-in (a new `credit_profile_sharing_enabled`
  boolean on `users`, off by default, toggled by an explicit WhatsApp
  intent like "share my credit profile" or via a future dashboard
  toggle — for now, a simple opt-in flag set via API is enough to
  demonstrate consent-gating exists structurally, not just as a
  promise).
- A partner-scoped access token (stub this simply — a static bearer
  token configured via env var for the demo, clearly documented as
  "in production this would be a proper OAuth2 client-credentials flow
  issued to Wema specifically," not built out fully now).

Returns aggregate, non-identifying-of-third-parties data only:
```json
{
  "trader_name": "Musa Trading",
  "account_age_days": 42,
  "total_credit_issued_minor": 68400000,
  "total_collected_minor": 59200000,
  "on_time_payment_rate": 0.87,
  "active_customer_count": 12,
  "outstanding_minor": 9200000
}
```

Never include customer names, phone numbers, or individual transaction
detail in this response — this is a trader's aggregate creditworthiness
signal, not a data export of their customers' private information.
That boundary matters for the same reason spec §36 already cares about
minimizing exposure of customer data internally.

## Why this is the right scope for the time available

A real bank API integration, OAuth handshake, webhook-based credit
bureau submission, or anything resembling an actual production
integration is not achievable credibly in the hours left — attempting
it risks either not finishing or finishing something fragile that
undermines trust in the rest of the (actually solid) demo. A well-
scoped, honestly-framed *pathway* — narrative plus one real,
consent-gated, working endpoint — is both achievable and, frankly, the
more credible thing to show judges: it demonstrates the team understood
the actual constraints of bank integration (consent, data minimization,
never touching money movement) rather than hand-waving past them.
