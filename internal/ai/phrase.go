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
// capabilityListText is docs/BRIEF-polish-and-hardening.md #2's exact
// replacement copy — verbatim in English; translated at the same
// quality bar as every other table in this file for the other four
// languages — minus its opening line, which withCapabilityList's own
// per-context prefix supplies (helpText's prefix reproduces #2's exact
// header; unsupportedRequestText's prefix keeps 1c's required "I can't
// do that yet" honest-decline framing instead). Factored out so every
// path that shows the capability list (HELP, the unsupported-request
// decline, the greeting menu's Help button) shows byte-identical item
// wording, never versions that drift apart over time.
var capabilityListText = map[Language]string{
	LangEnglish: "*Record a sale on credit*\n" +
		"e.g. \"Chinedu took 5k, pays Friday\"\n\n" +
		"*Log a payment*\n" +
		"e.g. \"Chinedu paid 5k\"\n\n" +
		"*Review outstanding balances*\n" +
		"e.g. \"Who owes me?\"\n\n" +
		"*Check a specific customer*\n" +
		"e.g. \"How much does Chinedu owe?\"\n\n" +
		"Tell me in your own words, I'll take it from there.",
	LangPidgin: "*Record sale wey dey on credit*\n" +
		"e.g. \"Chinedu carry 5k, e go pay Friday\"\n\n" +
		"*Log payment*\n" +
		"e.g. \"Chinedu pay 5k\"\n\n" +
		"*Check outstanding balance*\n" +
		"e.g. \"Who owe me?\"\n\n" +
		"*Check one customer*\n" +
		"e.g. \"How much Chinedu owe?\"\n\n" +
		"Tell me for your own way, I go take it from there.",
	LangYoruba: "*Ṣàkọsílẹ̀ ọjà tí a tà lórí gbèsè*\n" +
		"e.g. \"Chinedu gba 5k, yóò san ní Friday\"\n\n" +
		"*Ṣàkọsílẹ̀ ìsanwó*\n" +
		"e.g. \"Chinedu san 5k\"\n\n" +
		"*Ṣàyẹ̀wò gbèsè tó ṣẹ́kù*\n" +
		"e.g. \"Ta ló jẹ mí ní gbèsè?\"\n\n" +
		"*Ṣàyẹ̀wò oníbàárà kan pàtó*\n" +
		"e.g. \"Èló ni Chinedu jẹ mí?\"\n\n" +
		"Sọ ọ́ ní ọ̀nà tìẹ, màá tẹ̀síwájú láti ibẹ̀.",
	LangIgbo: "*Dekọọ ahịa e refuru na ụgwọ*\n" +
		"e.g. \"Chinedu were 5k, ọ ga-akwụ na Friday\"\n\n" +
		"*Dekọọ ịkwụ ụgwọ*\n" +
		"e.g. \"Chinedu kwụrụ 5k\"\n\n" +
		"*Lelee ụgwọ fọdụrụ*\n" +
		"e.g. \"Onye ji m ụgwọ?\"\n\n" +
		"*Lelee otu onye ahịa*\n" +
		"e.g. \"Ego ole Chinedu ji m?\"\n\n" +
		"Gwa m n'okwu gị, m ga-esite ebe ahụ gaa n'ihu.",
	LangHausa: "*Rikodin siyar da kaya bisa bashi*\n" +
		"e.g. \"Chinedu ya dauka 5k, zai biya Jumma'a\"\n\n" +
		"*Rikodin biya*\n" +
		"e.g. \"Chinedu ya biya 5k\"\n\n" +
		"*Duba bashin da ya rage*\n" +
		"e.g. \"Wa ke bin ni bashi?\"\n\n" +
		"*Duba wani abokin ciniki*\n" +
		"e.g. \"Nawa Chinedu ke bi na bashi?\"\n\n" +
		"Faɗa mini a hanyarka, zan ci gaba daga nan.",
}

var helpText = withCapabilityList(map[Language]string{
	LangEnglish: "Here's what I can help you manage:\n\n",
	LangPidgin:  "Na dis I fit help you manage:\n\n",
	LangYoruba:  "Ìwọ̀nyí ni mo lè ràn ọ́ lọ́wọ́ láti bójú tó:\n\n",
	LangIgbo:    "Ihe ndị a ka m nwere ike inyere gị aka ijikwa:\n\n",
	LangHausa:   "Ga abin da zan iya taimaka maka ka lura da su:\n\n",
})

// unsupportedRequestText is docs/BRIEF-critical-fixes-and-reminders.md
// #1c's honest decline — "I can't do that yet — here's what I can help
// with: [capability list]" — for IntentUnsupported, a request for
// something Ruby genuinely doesn't do (invoicing, inventory, etc.).
// Distinct from reminderUnsupportedText: that one is specific to a
// recognized-but-not-yet-user-invokable feature, this one is the
// general "that's not a thing I do at all" case.
var unsupportedRequestText = withCapabilityList(map[Language]string{
	LangEnglish: "I can't do that yet — here's what I can help with:\n\n",
	LangPidgin:  "I no fit do dat one yet — na dis I fit help you with:\n\n",
	LangYoruba:  "Mi ò tíì lè ṣe bẹ́ẹ̀ — ìwọ̀nyí ni mo lè ràn ọ́ lọ́wọ́ pẹ̀lú:\n\n",
	LangIgbo:    "Enweghị m ike ime nke ahụ ugbu a — ihe ndị a ka m nwere ike inyere gị aka na ha:\n\n",
	LangHausa:   "Ba zan iya yin haka ba tukuna — ga abin da zan iya taimaka maka da shi:\n\n",
})

// withCapabilityList concatenates a per-language prefix in front of the
// shared capabilityListText body for every language, in one place, so
// helpText/unsupportedRequestText can never end up with a language key
// present in one but missing from the other.
func withCapabilityList(prefixes map[Language]string) map[Language]string {
	out := make(map[Language]string, len(prefixes))
	for lang, prefix := range prefixes {
		out[lang] = prefix + capabilityListText[lang]
	}
	return out
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

// editPromptText and cancelledText answer the "Edit"/"Cancel" buttons
// on a confirmation prompt (docs/BRIEF-interactive-messages.md). Fixed
// strings, not a Phraser call, for the same reason as helpText et al.:
// no dynamic outcome to phrase.
var editPromptText = map[Language]string{
	LangEnglish: "No problem — just send the correct details and I'll try again.",
	LangPidgin:  "No wahala — just send the correct details make I try again.",
	LangYoruba:  "Kò sí ìṣòro — fi àwọn kúlẹ̀kúlẹ̀ tí ó tọ́ ránṣẹ́, kí n gbìyànjú lẹ́ẹ̀kan sí i.",
	LangIgbo:    "Ọ dịghị nsogbu — zipu nkọwa ziri ezi, m ga-anwa ọzọ.",
	LangHausa:   "Babu matsala — kawai aika daidaitattun bayanai, in sake gwadawa.",
}

var cancelledText = map[Language]string{
	LangEnglish: "Okay, cancelled — nothing was recorded.",
	LangPidgin:  "Okay, I cancel am — nothing enter book.",
	LangYoruba:  "Ó dára, ó ti fagilé — a kò ṣàkọsílẹ̀ ohunkóhun.",
	LangIgbo:    "Ọ dị mma, ekagbuola ya — ọ dịghị ihe edekọrọ.",
	LangHausa:   "To, an soke — ba a rubuta komai ba.",
}

// smallTalkText is docs/BRIEF-critical-fixes-and-reminders.md #3a's
// brief, warm reply for genuine chit-chat with no real request behind
// it — fixed, not Phraser-generated (no dynamic outcome to phrase,
// same reasoning as helpText et al.), and deliberately short: it
// shouldn't read as a fresh pitch (docs/BRIEF-response-quality.md #2's
// same concern, just triggered by small talk instead of a greeting).
var smallTalkText = map[Language]string{
	LangEnglish: "I'm doing well, thanks for asking! How can I help with your credit sales today?",
	LangPidgin:  "I dey kampe, thank you for ask! How I fit help you with your credit sales today?",
	LangYoruba:  "Àlàáfíà ni, o ṣé fún bíbéèrè! Kín ni mo lè ràn ọ́ lọ́wọ́ pẹ̀lú ọjà rẹ tí a ta lórí gbèsè lónìí?",
	LangIgbo:    "Adị m mma, daalụ maka ịjụ! Kedu ka m ga-esi nyere gị aka na ahịa gị ị na-ere n'ụgwọ taa?",
	LangHausa:   "Ina lafiya, na gode da tambaya! Ta yaya zan iya taimaka maka da tallace-tallacenka na bashi yau?",
}

// selfQueryNameText answers docs/BRIEF-critical-fixes-and-reminders.md
// #3b's required case directly from users.name — %s is the name, read
// straight from the account, never guessed or phrased by the model.
var selfQueryNameText = map[Language]string{
	LangEnglish: "Your name is *%s*.",
	LangPidgin:  "Your name na *%s*.",
	LangYoruba:  "Orúkọ rẹ ni *%s*.",
	LangIgbo:    "Aha gị bụ *%s*.",
	LangHausa:   "Sunanka *%s* ne.",
}

// slotFillCustomerText and slotFillAmountText/slotFillAmountWithNameText
// are the interactive slot-filling section's own questions
// (docs/BRIEF-critical-fixes-and-reminders.md) — fixed, not Phraser-
// generated, same reasoning as every other pending-state question
// built this session (identity confirmation, customer-signal request,
// reminder phone request): deterministic, no AI-call latency/cost, and
// no grounding risk for something this simple. %s in the "with name"
// variant is the customer name, when it's already known.
var slotFillCustomerText = map[Language]string{
	LangEnglish: "Who is this for?",
	LangPidgin:  "Na who be dis for?",
	LangYoruba:  "Ta ni èyí ṣe fún?",
	LangIgbo:    "Onye ka nke a bụ?",
	LangHausa:   "Wanene wannan?",
}

var slotFillAmountText = map[Language]string{
	LangEnglish: "How much?",
	LangPidgin:  "How much?",
	LangYoruba:  "Èló?",
	LangIgbo:    "Ego ole?",
	LangHausa:   "Nawa?",
}

var slotFillAmountWithNameText = map[Language]string{
	LangEnglish: "How much for *%s*?",
	LangPidgin:  "How much for *%s*?",
	LangYoruba:  "Èló fún *%s*?",
	LangIgbo:    "Ego ole maka *%s*?",
	LangHausa:   "Nawa domin *%s*?",
}

// slotFillCustomerReaskText and slotFillAmountReaskText are edge case
// #3's more direct re-ask when a reply "obviously doesn't match what
// was asked" — mirrors onboarding.go's nameReaskText, same reasoning:
// by the time Ruby is re-asking, it's already responding to a reply
// that didn't work, so there's no reliable language signal beyond
// what's already established for this pending intent.
var slotFillCustomerReaskText = map[Language]string{
	LangEnglish: "I just need to know who this is for — what's their name?",
	LangPidgin:  "I just need to know who dis be for — wetin be their name?",
	LangYoruba:  "Mo kàn nílò láti mọ ẹni tí èyí ṣe fún — kí ni orúkọ wọn?",
	LangIgbo:    "Naanị achọrọ m ịma onye nke a bụ — gịnị bụ aha ha?",
	LangHausa:   "Ina bukatan sanin wanda wannan yake — menene sunansu?",
}

var slotFillAmountReaskText = map[Language]string{
	LangEnglish: "I just need the amount — how much was it?",
	LangPidgin:  "I just need the amount — how much e be?",
	LangYoruba:  "Mo kàn nílò iye owó náà — èló ni?",
	LangIgbo:    "Naanị achọrọ m ego ole ọ bụ — ego ole ọ bụ?",
	LangHausa:   "Ina bukatan adadin kudi ne kawai — nawa ne?",
}

// noOutstandingDebtsText, dueLabelText, and totalOutstandingLabelText
// back the deterministic LIST_OUTSTANDING_DEBTS formatting in format.go
// (docs/BRIEF-response-quality.md #4) — fixed strings, not a Phraser
// call, same reasoning as helpText et al.
var noOutstandingDebtsText = map[Language]string{
	LangEnglish: "You're all clear — no one owes you right now.",
	LangPidgin:  "You clear — nobody owe you now.",
	LangYoruba:  "O ti yege — kò sí ẹnikẹ́ni tó jẹ ọ́ ní gbèsè báyìí.",
	LangIgbo:    "Ọ dị gị mma — ọ dịghị onye ji gị ụgwọ ugbu a.",
	LangHausa:   "Kana da lafiya — babu wanda ke bin ka bashi a yanzu.",
}

var dueLabelText = map[Language]string{
	LangEnglish: "due",
	LangPidgin:  "e go pay",
	LangYoruba:  "yóò san ní",
	LangIgbo:    "kwụọ na",
	LangHausa:   "biya a",
}

var totalOutstandingLabelText = map[Language]string{
	LangEnglish: "Total outstanding:",
	LangPidgin:  "Total wey remain:",
	LangYoruba:  "Àpapọ̀ tó ṣẹ́kù:",
	LangIgbo:    "Mkpokọta fọdụrụ:",
	LangHausa:   "Jimillar da ta rage:",
}

// noCustomersText and customerListHeaderText back the deterministic
// LIST_CUSTOMERS formatting in format.go
// (docs/BRIEF-critical-fixes-and-reminders.md #2a) — same reasoning as
// noOutstandingDebtsText: a genuine empty-state message, not a Phraser
// call left to improvise around an empty/omitted items field.
var noCustomersText = map[Language]string{
	LangEnglish: "You haven't added any customers yet.",
	LangPidgin:  "You never add any customer yet.",
	LangYoruba:  "O ò tíì fi oníbàárà kankan kún un.",
	LangIgbo:    "Ị na-etinyebeghị onye ahịa ọ bụla.",
	LangHausa:   "Ba ka ƙara wani abokin ciniki ba tukuna.",
}

var customerListHeaderText = map[Language]string{
	LangEnglish: "Your customers:",
	LangPidgin:  "Your customers:",
	LangYoruba:  "Àwọn oníbàárà rẹ:",
	LangIgbo:    "Ndị ahịa gị:",
	LangHausa:   "Abokan cinikinka:",
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
