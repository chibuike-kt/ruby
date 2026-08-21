// Package ai turns a trader's WhatsApp message (text or voice) into an
// action against the already-proven customer/debt/payment services (spec
// §9/§10). The one rule every file here answers to (decisions.md #5):
// nothing straight off the model ever reaches a service call. RawIntent
// is untrusted input; Validator turns it into an Action, a description of
// what would happen, never a mutation itself; only Processor.Execute,
// after any confidence/confirmation gate passes, calls a service.
package ai

// IntentType is one of the structured intents spec §9 lists, minus
// DISAMBIGUATE_CUSTOMER and EXPORT_RECORDS — per docs/BRIEF-ai-intent.md,
// disambiguation is Ruby-initiated from a service-layer AmbiguousError
// (decisions.md #8), not something the model needs to detect, and export
// is out of scope for this hackathon (spec §48).
type IntentType string

const (
	IntentCreateDebt           IntentType = "CREATE_DEBT"
	IntentRecordPayment        IntentType = "RECORD_PAYMENT"
	IntentListCustomers        IntentType = "LIST_CUSTOMERS"
	IntentGetCustomerBalance   IntentType = "GET_CUSTOMER_BALANCE"
	IntentListOutstandingDebts IntentType = "LIST_OUTSTANDING_DEBTS"
	IntentGetTotalOutstanding  IntentType = "GET_TOTAL_OUTSTANDING"
	IntentGetPaymentSummary    IntentType = "GET_PAYMENT_SUMMARY"
	IntentCreateReminder       IntentType = "CREATE_REMINDER"
	IntentCancelReminder       IntentType = "CANCEL_REMINDER"
	IntentConfirmAction        IntentType = "CONFIRM_ACTION"
	IntentHelp                 IntentType = "HELP"

	// IntentGetCustomerStatement is docs/BRIEF-disambiguation-reminders-
	// statements.md Tier 3: a real per-customer account history (every
	// debt with its item description and date, every payment against
	// each, and the current outstanding balance) — replacing what used
	// to be an honest UNSUPPORTED decline now that the feature is real.
	// Recognized from natural phrasing ("give me a breakdown for
	// Emmanuel", "summarize what he owes", "show his account"), not
	// only the literal word "statement."
	IntentGetCustomerStatement IntentType = "GET_CUSTOMER_STATEMENT"

	// IntentUnsupported is docs/BRIEF-critical-fixes-and-reminders.md
	// #1c's structural guard: a request for something Ruby genuinely
	// doesn't do (invoicing, inventory, expense tracking, anything
	// outside bookkeeping for credit sales) must land here, explicitly,
	// rather than being force-fit into the closest-sounding real intent
	// and then improvised into a fake multi-turn flow via slot-filling.
	// Distinct from HELP (an explicit "what can you do" ask).
	IntentUnsupported IntentType = "UNSUPPORTED"

	// IntentSmallTalk and IntentSelfQuery are #3a/#3b's split of what
	// used to all collapse into the same catch-all: genuine chit-chat
	// ("how are you") gets a brief warm reply, not the capability list;
	// a question Ruby can actually answer from its own stored data
	// ("what's my name") gets a real answer, not a decline.
	IntentSmallTalk IntentType = "SMALL_TALK"
	IntentSelfQuery IntentType = "SELF_QUERY"

	// IntentDataSafety is docs/BRIEF-research-hardening-standard.md Part
	// 4's trust-building answer: "is my data safe"/"is this secure" as a
	// real, classified intent — a plain, factual answer built once as
	// fixed text (see dataSafetyText), never left to the Phraser to
	// improvise or overclaim a certification Ruby doesn't have. Distinct
	// from HELP (what Ruby does) and SMALL_TALK (no real question behind
	// it) — this is a genuine question with a genuine answer.
	IntentDataSafety IntentType = "DATA_SAFETY"
)

// Confidence mirrors spec §23: the model's own signal for how sure it is
// about names/numbers/dates it extracted, mainly relevant to voice input.
type Confidence string

const (
	ConfidenceHigh Confidence = "high"
	ConfidenceLow  Confidence = "low"
)

// Language is one of the five codes spec §22 lists for this hackathon.
// pcm is Nigerian Pidgin's correct ISO 639-3 code (docs/BRIEF-ai-intent.md
// is explicit that this isn't a code to invent).
type Language string

const (
	LangEnglish Language = "en"
	LangPidgin  Language = "pcm"
	LangYoruba  Language = "yo"
	LangIgbo    Language = "ig"
	LangHausa   Language = "ha"
)
