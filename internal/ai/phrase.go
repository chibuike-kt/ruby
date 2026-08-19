package ai

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
type PhraseInput struct {
	Event            PhraseEvent
	Language         Language
	CustomerName     string
	AmountMinor      int64
	OutstandingMinor int64
	CollectedMinor   int64
	AttemptedMinor   int64
	DueDateISO       string
	Description      string
	Items            []string // display list: candidate customers, customer names, outstanding debts — meaning depends on Event
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

func fixedText(table map[Language]string, lang Language) string {
	if s, ok := table[lang]; ok {
		return s
	}
	return table[LangEnglish]
}
