# Brief — AI intent extraction

Builds on the WhatsApp webhook (receives/stores messages) and the
financial core (customer/debt/payment/ledger, all verified end-to-end
over real HTTP). This slice is what actually understands what a
trader said and turns it into action — the piece that makes this feel
like Ruby rather than a message log.

## Models — confirmed current as of this month, don't use older names

- **Text intent extraction**: `gpt-5.6-terra` — balances quality and
  cost for structured extraction, don't reach for `gpt-5.6-sol`
  (flagship reasoning/coding) here, this task doesn't need it. If
  cost becomes a real concern under hackathon budget, `gpt-5.6-luna`
  is the cheaper tier and likely still adequate for this schema size.
- **Voice transcription**: `gpt-transcribe` — OpenAI's current
  recommended default, not `whisper-1` or `gpt-4o-transcribe` (older,
  higher error rate, more expensive). Accepts mp3/mp4/mpeg/mpga/m4a/
  wav/webm, up to 25MB.
- **Use Structured Outputs with `strict: true`**, not free-form JSON
  parsing or the older JSON mode — this is what guarantees the model's
  output actually matches the intent schema below, not just "usually
  parses as JSON."

## The critical format mismatch — handle this explicitly

WhatsApp voice notes arrive as `.ogg` (Opus codec) via the Media API.
`gpt-transcribe` does not accept `.ogg`. This means the pipeline is:

```
WhatsApp Media API (.ogg)
    -> download
    -> transcode to .mp3 or .wav (ffmpeg)
    -> gpt-transcribe
    -> text
    -> intent extraction (same path as a text message from here)
```

Don't skip the transcode step assuming the API accepts ogg directly —
it doesn't, and this is exactly the kind of assumption worth verifying
against the current docs rather than the spec's original pipeline
diagram (which was written before this format constraint was known).

## Intent schema (spec §9)

Structured Outputs schema, one call, covering all intents from the
spec: `CREATE_DEBT`, `RECORD_PAYMENT`, `LIST_CUSTOMERS`,
`GET_CUSTOMER_BALANCE`, `LIST_OUTSTANDING_DEBTS`,
`GET_TOTAL_OUTSTANDING`, `GET_PAYMENT_SUMMARY`, `CREATE_REMINDER`,
`CANCEL_REMINDER`, `CONFIRM_ACTION`, `HELP`. Skip
`DISAMBIGUATE_CUSTOMER` and `EXPORT_RECORDS` as extractable intents —
disambiguation is something Ruby *initiates* based on service-layer
responses (decisions.md #8), not something the AI needs to detect from
a message; export is out of scope for this hackathon per spec §48.

Fields: `intent`, `customer_name` (nullable), `amount_minor` (nullable
int, already converted to kobo — don't have the model return naira
and convert later, do the conversion in the prompt/schema so `money.Money`
is the only place that logic lives), `description` (nullable),
`due_date_iso` (nullable, resolved against `DEFAULT_TIMEZONE` from
config — "Friday" becomes an actual date the model computes, not a
string Ruby has to parse later), `confidence` (enum: high/low, spec
§23's "use confirmation for uncertain instructions").

## The AI boundary — this is the one rule that matters most here

Per decisions.md #5: **`internal/ai`'s output never reaches a service
or the DB directly.** The intent struct from OpenAI's response is not
itself the DTO the customer/debt/payment services accept — it passes
through an explicit validator first (amount is positive, if a signal
exists for customer resolution use `customer.Service.Resolve`, not the
raw name string; date isn't in the past for a due date unless that's
the wording, per spec §21's overdue handling). If you're tempted to
have a handler call `debt.Service.Create` directly with fields lifted
from the AI response, stop — that's the exact thing this boundary
exists to prevent.

## Low-confidence handling (spec §23)

When `confidence` is `low` (voice transcription is the main source of
this — names, numbers, currency, and dates are all things ASR gets
wrong), don't act on the intent. Respond with what was understood and
ask for confirmation, matching the `CONFIRM_ACTION` intent as the
expected next message. High-confidence intents on low-risk queries
(read-only things like `LIST_CUSTOMERS`, `GET_CUSTOMER_BALANCE`) can
skip confirmation even if the model itself reports lower confidence —
confirmation exists to protect financial correctness, not to slow down
every interaction equally.

## Wiring into what already exists

- Customer resolution: use `customer.Service.Resolve` with whatever
  signal is available from the message (phone if the trader gave one,
  otherwise name) — this is where the Redis conversational-context
  store from the API/Redis slice actually gets used for the first
  time, for pronoun resolution ("he paid me" -> last resolved
  customer for this `user_id`, spec §8 signal 4).
- Ambiguous resolution: if `Resolve` returns `AmbiguousError`, don't
  guess — send the disambiguation prompt back to the trader (decisions
  #8's hint logic already builds this), and store enough context to
  resolve the reply as an answer to that specific ambiguity, not a new
  message.
- Once an intent is validated, call the same services already proven
  correct — `debt.Service.Create`, `payment.Service.Record`, etc. This
  slice should not duplicate any financial logic, only translate
  language into calls to logic that already exists and is tested.

## Multilingual support — English, Nigerian Pidgin, Yoruba, Igbo, Hausa

Full scope per spec §22's original list, not English-only. This is a
real product differentiator for the actual target users, worth the
extra build cost.

- **Language codes**: `en`, `pcm` (Nigerian Pidgin, correct ISO 639-3
  code — don't invent one), `yo` (Yoruba), `ig` (Igbo), `ha` (Hausa).
- **Detection is part of the same structured-output call**, not a
  separate step — add a `language` field to the intent schema
  (enum of the five codes above). GPT-5.6 is multilingual and can
  reliably identify which of these a message is in as part of the
  same extraction call; don't add a separate detection round-trip.
- **Voice transcription**: pass language hints for all five codes to
  `gpt-transcribe`'s language-hints parameter — this measurably
  improves accuracy over letting it guess cold, per the current docs.
  The transcription response also reports detected language directly;
  prefer that over re-detecting from the transcript text if both are
  available.
- **Code-switching is normal, not an edge case.** Real Nigerian
  informal speech mixes English and Pidgin constantly ("Chinedu carry
  two carton noodles, e go pay Friday" is realistic input, not a
  stress test). The schema's `language` field should capture the
  dominant/primary language for response purposes, but extraction
  itself needs to work on mixed input without the schema forcing a
  single-language assumption.

## Response generation — revised for multilingual output

Templated English strings alone don't work anymore, since a Yoruba or
Hausa trader needs a natural-sounding reply in their language, not a
translated-by-template one — hand-written templates in five languages
risk sounding stiff or simply wrong to a native speaker, which is
worse than not supporting the language at all.

Use a **second, narrowly-scoped AI call** (cheap tier — `gpt-5.6-luna`
is fine here, this is a phrasing task, not a reasoning one) that:

- Receives only the **already-decided outcome** as structured input
  — e.g. `{event: "DEBT_CREATED", customer: "Chinedu", amount_minor:
  7500000, due_date: "2026-08-21", language: "yo"}` — never the raw
  trader message, never a decision to make.
- Returns **only phrasing** in the target language. It does not
  choose what happened, doesn't see account/financial data beyond
  what's in that outcome object, and its output is never parsed back
  into anything financial — it's terminal, going straight to the
  WhatsApp reply.
- This keeps the boundary from decisions.md #5 intact: the first AI
  call's output still can't reach a service unvalidated, and this
  second call operates strictly after the mutation, on data the
  backend already confirmed as true. It's not a second path around
  the boundary, it's cosmetic on top of it.

For intents with no dynamic outcome (like `HELP`), a fixed
per-language string is fine and doesn't need a model call — reserve
the phrasing call for actual transactional confirmations and error
messages where the content varies per request.

## Testing

- Schema validation: a battery of realistic message variations (the
  spec's own examples are a good starting set) parsed correctly.
- The AI boundary: a test proving a handcrafted "malicious" or
  malformed AI response (negative amount, nonexistent customer,
  intent that doesn't match the message) never reaches a service call
  unvalidated — spec §45's "invalid AI output" security test.
- Low-confidence path: a mocked low-confidence response results in a
  confirmation prompt, not an executed action.
- Voice pipeline: an ogg fixture file goes through transcode ->
  transcribe -> extract successfully (or at minimum, mock the
  transcode/transcribe boundary if a real audio fixture is impractical
  to commit to the repo — use your judgment, don't let this block on
  finding the perfect test audio file).
- Multilingual: at minimum one realistic message per language
  (English, Pidgin, Yoruba, Igbo, Hausa) correctly extracting intent
  and detecting language, plus one deliberately code-switched example.
  For the response-phrasing call, a test confirming it never receives
  the raw trader message — only the structured outcome object — so
  the boundary can't quietly erode later.

## Explicitly out of scope

Reminders (next slice), export, a translation/response layer for
languages beyond the five listed above.
