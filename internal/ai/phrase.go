package ai

import (
	"regexp"
	"strconv"
	"strings"
)

// PhraseEvent names the already-decided outcome a Phraser call turns
// into natural language. There is deliberately no field anywhere in this
// file that could carry a raw trader message — see plan decision #7 and
// TestPhraseInput_NeverCarriesRawText.
type PhraseEvent string

const (
	EventDebtCreated        PhraseEvent = "DEBT_CREATED"
	EventPaymentRecorded    PhraseEvent = "PAYMENT_RECORDED"
	EventOverpaymentPrompt  PhraseEvent = "OVERPAYMENT_PROMPT"
	EventConfirmationNeeded PhraseEvent = "CONFIRMATION_NEEDED"
	EventAmbiguousCustomer  PhraseEvent = "AMBIGUOUS_CUSTOMER"
	EventCustomerBalance    PhraseEvent = "CUSTOMER_BALANCE"
	EventCustomerList       PhraseEvent = "CUSTOMER_LIST"
	EventOutstandingList    PhraseEvent = "OUTSTANDING_LIST"
	EventTotalOutstanding   PhraseEvent = "TOTAL_OUTSTANDING"
	EventPaymentSummary     PhraseEvent = "PAYMENT_SUMMARY"
	EventCustomerNotFound   PhraseEvent = "CUSTOMER_NOT_FOUND"
	EventNoOutstandingDebt  PhraseEvent = "NO_OUTSTANDING_DEBT"
	EventAmountRequired     PhraseEvent = "AMOUNT_REQUIRED"
	EventCustomerRequired   PhraseEvent = "CUSTOMER_REQUIRED"
)

// PhraseInput is the only thing a Phraser call ever sees: an
// already-decided outcome plus the fields needed to phrase it, and the
// target Language. Every field is a plain value the backend already
// confirmed as true — no raw text field exists on this type (see
// TestPhraseInput_NeverCarriesRawText).
//
// The amount fields are *int64 with omitempty, not int64: a nil pointer
// (omitted from the JSON the model sees) means "not part of this
// outcome," distinct from a populated zero, which means "this is
// genuinely zero" (e.g. a debt that's just been fully settled). Go's
// zero value for int64 is 0, indistinguishable from a real ₦0 — sending
// that as amount_minor/outstanding_minor for an event where it isn't
// actually meaningful is exactly the bug class this guards against: the
// model would otherwise see a real-looking field and phrase around it.
type PhraseInput struct {
	Event            PhraseEvent `json:"event"`
	Language         Language    `json:"language"`
	CustomerName     string      `json:"customer_name,omitempty"`
	AmountMinor      *int64      `json:"amount_minor,omitempty"`
	OutstandingMinor *int64      `json:"outstanding_minor,omitempty"`
	CollectedMinor   *int64      `json:"collected_minor,omitempty"`
	AttemptedMinor   *int64      `json:"attempted_minor,omitempty"`
	DueDateISO       string      `json:"due_date_iso,omitempty"`
	Description      string      `json:"description,omitempty"`
	Items            []string    `json:"items,omitempty"` // display list: candidate customers, customer names, outstanding debts — meaning depends on Event
}

// helpText, reminderUnsupportedText, nothingToConfirmText, and
// unsupportedMessageTypeText are fixed per-language strings, not a
// Phraser call — the brief's own carve-out: intents with no dynamic
// outcome don't need a model call, and reusing the phrasing call for
// these would risk it drifting toward seeing raw trader text over time.
// Kept deliberately short and simple to bound translation-quality risk.
var helpText = map[Language]string{
	LangEnglish: "I can record a debt, record a payment, or tell you who owes you. Just tell me in plain language, e.g. \"Chinedu took 5k, pays Friday\" or \"Chinedu paid 5k\".",
	LangPidgin:  "I fit record debt, record payment, or tell you who still owe you. Just talk am plain, like \"Chinedu carry 5k, e go pay Friday\" or \"Chinedu pay 5k\".",
	LangYoruba:  "Mo le ṣe àkọsílẹ̀ gbèsè, ìsanwó, tàbí sọ ẹni tó jẹ ọ́ ní gbèsè. Sọ fún mi ní ìjìnlẹ̀ èdè, bí i \"Chinedu gba 5k, yóò san ní Friday\" tàbí \"Chinedu san 5k\".",
	LangIgbo:    "Enwere m ike ịdekọ ụgwọ, ịdekọ ịkwụ ụgwọ, ma ọ bụ gwa gị onye ji gị ụgwọ. Naanị gwa m n'okwu efu, dịka \"Chinedu were 5k, ọ ga-akwụ na Friday\" ma ọ bụ \"Chinedu kwụrụ 5k\".",
	LangHausa:   "Zan iya rikodin bashi, rikodin biya, ko in gaya maka wanda ke bin ka bashi. Ka gaya mini a sauƙaƙe, misali \"Chinedu ya dauka 5k, zai biya Jumma'a\" ko \"Chinedu ya biya 5k\".",
}

var reminderUnsupportedText = map[Language]string{
	LangEnglish: "Reminders aren't available yet, but I've noted your request — check back soon.",
	LangPidgin:  "Reminder no dey available now, but I don note your request — check back soon.",
	LangYoruba:  "Ìránnilétí kò tíì wà, ṣùgbọ́n mo ti kọ ìbéèrè rẹ sílẹ̀ — wá wo lẹ́ẹ̀kan sí i láìpẹ́.",
	LangIgbo:    "Ncheta anaghị arụ ọrụ ugbu a, mana edekọla m arịrịọ gị — lelata n'oge na-adịghị anya.",
	LangHausa:   "Abubuwan tunatarwa ba su samu ba tukuna, amma na lura da bukatarka — duba nan gaba kadan.",
}

var nothingToConfirmText = map[Language]string{
	LangEnglish: "There's nothing waiting for your confirmation right now.",
	LangPidgin:  "Nothing dey wait for your confirmation now.",
	LangYoruba:  "Kò sí ohunkóhun tí ó dúró de ìjẹ́rìí rẹ ní báyìí.",
	LangIgbo:    "Ọ dịghị ihe na-echere nkwenye gị ugbu a.",
	LangHausa:   "Babu wani abu da ke jiran tabbatarwa daga gare ka a yanzu.",
}

var unsupportedMessageTypeText = map[Language]string{
	LangEnglish: "I can only understand text and voice messages right now.",
	LangPidgin:  "I fit only understand text and voice message now.",
	LangYoruba:  "Ọ̀rọ̀ tàbí ohùn nìkan ni mo lè lóye ní báyìí.",
	LangIgbo:    "Naanị ozi edere ede ma ọ bụ olu ka m nwere ike ịghọta ugbu a.",
	LangHausa:   "Zan iya fahimtar rubutu ko sako na murya kawai a yanzu.",
}

// genericErrorText is spec §37's "never respond with an unexplained
// technical error" fallback — used both when no Phraser is configured
// and when the Phraser call itself fails, so an AI outage never leaves a
// trader staring at silence or a raw error.
var genericErrorText = map[Language]string{
	LangEnglish: "Something went wrong while recording that. Your existing records are safe. Please try again.",
	LangPidgin:  "Something happen while I dey try record am. Your old records safe. Abeg try again.",
	LangYoruba:  "Àṣìṣe kan ṣẹlẹ̀ nígbà tí mo ń gbìyànjú láti ṣàkọsílẹ̀ èyí. Àwọn àkọsílẹ̀ rẹ tẹ́lẹ̀ wà láìséwu. Jọ̀wọ́ gbìyànjú lẹ́ẹ̀kan sí i.",
	LangIgbo:    "Ihe adịghị mma mere ka m na-adekọ nke ahụ. Ndekọ gị ndị dị adị nọ n'enweghị nsogbu. Biko nwaa ọzọ.",
	LangHausa:   "Wani abu ya faru ba daidai ba yayin ƙoƙarin yin rikodin hakan. Bayanan da kake da su tuni suna lafiya. Da fatan za a sake gwadawa.",
}

// mutationSucceededFallbackText is used instead of genericErrorText when
// a grounding check (see isGrounded) rejects a phrased reply for an
// event that already committed a real mutation (DEBT_CREATED,
// PAYMENT_RECORDED). genericErrorText says "please try again," which
// would be actively wrong here — the trader might retry and create a
// duplicate. This fallback confirms success without repeating any
// number, so it can never itself violate the grounding it exists to
// enforce.
var mutationSucceededFallbackText = map[Language]string{
	LangEnglish: `Done — recorded. Message "balance" to see the full details.`,
	LangPidgin:  `Done — I don record am. Message "balance" make you see the full details.`,
	LangYoruba:  `Ti ṣe — ó ti wà ní àkọsílẹ̀. Fi ránṣẹ́ "balance" láti rí kúlẹ̀kúlẹ̀ rẹ̀.`,
	LangIgbo:    `Emeela — edekọla ya. Zipu "balance" ka ị hụ nkọwa zuru ezu.`,
	LangHausa:   `An yi — an rubuta shi. Aika "balance" don ganin cikakkun bayanai.`,
}

// fallbackText picks the safe reply for when a Phraser call fails or is
// rejected by isGrounded — never genericErrorText's "please try again"
// for an event that already committed (see mutationSucceededFallbackText).
func fallbackText(event PhraseEvent, lang Language) string {
	switch event {
	case EventDebtCreated, EventPaymentRecorded:
		return fixedText(mutationSucceededFallbackText, lang)
	default:
		return fixedText(genericErrorText, lang)
	}
}

// numberToken matches a number as one unit, including its own thousand
// separators/decimal point ("75,000.00", "7500000", "0") — deliberately
// not just \d+, which would split "75,000" into "75" and "000" and force
// isGrounded to fall back to blanket-allowing small fragments like "0".
// Capturing the whole token means a genuine ₦0 must be grounded by an
// actual zero-valued field, never waved through as a formatting
// artifact — that distinction is exactly what this whole check exists
// for (see isGrounded's doc comment).
var numberToken = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

func stripSeparators(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// allowedNumbers collects every number the phrasing model was actually
// given, in every form it might reasonably render one in: minor units,
// major units (naira, i.e. minor/100 — NGN's 2-decimal display strips to
// the same digit string as the minor-unit form, but the undivided major
// form is also allowed for a "₦75,000" style rendering), and the
// individual components of a due date. Digits already present in
// free-text fields (Description, CustomerName, Items) the backend itself
// supplied are allowed too, since those aren't invented by the model —
// they were handed to it verbatim. There is no unconditional allowance
// for "0" — a field must actually be present and zero-valued (nil means
// "not part of this outcome," not zero) for isGrounded to accept it.
func allowedNumbers(input PhraseInput) map[string]bool {
	allowed := map[string]bool{}

	addAmount := func(minor *int64) {
		if minor == nil {
			return
		}
		v := *minor
		if v < 0 {
			v = -v
		}
		allowed[strconv.FormatInt(v, 10)] = true
		allowed[strconv.FormatInt(v/100, 10)] = true
	}
	addAmount(input.AmountMinor)
	addAmount(input.OutstandingMinor)
	addAmount(input.CollectedMinor)
	addAmount(input.AttemptedMinor)

	if input.DueDateISO != "" {
		for part := range strings.SplitSeq(input.DueDateISO, "-") {
			trimmed := strings.TrimLeft(part, "0")
			if trimmed == "" {
				trimmed = "0"
			}
			allowed[trimmed] = true
			allowed[part] = true
		}
	}

	scan := func(s string) {
		for _, m := range numberToken.FindAllString(s, -1) {
			digits := stripSeparators(m)
			allowed[digits] = true
			trimmed := strings.TrimLeft(digits, "0")
			if trimmed == "" {
				trimmed = "0"
			}
			allowed[trimmed] = true
		}
	}
	scan(input.Description)
	scan(input.CustomerName)
	for _, item := range input.Items {
		scan(item)
	}

	return allowed
}

// isGrounded reports whether every number in text traces back to
// allowedNumbers(input) — the backend-enforced half of plan decision #7:
// a Phraser call is "terminal" in the sense that its output is never
// parsed back into a financial action, but that never meant unchecked.
// A number the model states that isn't grounded in its own input is a
// boundary violation, not a phrasing choice, and Processor.phrase falls
// back to a fixed string rather than let it reach a trader.
func isGrounded(text string, input PhraseInput) bool {
	allowed := allowedNumbers(input)
	for _, m := range numberToken.FindAllString(text, -1) {
		digits := stripSeparators(m)
		if allowed[digits] {
			continue
		}
		trimmed := strings.TrimLeft(digits, "0")
		if trimmed == "" {
			trimmed = "0"
		}
		if allowed[trimmed] {
			continue
		}
		return false
	}
	return true
}

func fixedText(table map[Language]string, lang Language) string {
	if s, ok := table[lang]; ok {
		return s
	}
	return table[LangEnglish]
}
