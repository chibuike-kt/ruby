package ai

import (
	"fmt"
	"strings"
)

// greetingPhrases maps a normalized bare greeting to the language it
// signals. Checked before the AI extractor ever runs
// (docs/BRIEF-interactive-messages.md: "must not fall through to the
// financial-intent extractor") — a fixed, deterministic list is a
// stronger guarantee than depending on the model to classify a greeting
// correctly every single time, and it's the cheaper, faster path too.
var greetingPhrases = map[string]Language{
	"hi": LangEnglish, "hello": LangEnglish, "hey": LangEnglish, "hiya": LangEnglish, "howdy": LangEnglish,
	"morning": LangEnglish, "good morning": LangEnglish, "good afternoon": LangEnglish, "good evening": LangEnglish,
	"good day": LangEnglish, "greetings": LangEnglish, "hi there": LangEnglish, "hello there": LangEnglish,
	"hi ruby": LangEnglish, "hello ruby": LangEnglish, "hey ruby": LangEnglish, "good morning ruby": LangEnglish,

	"how far": LangPidgin, "how you dey": LangPidgin, "how body": LangPidgin, "wetin dey happen": LangPidgin,
	"abeg how far": LangPidgin, "good morning o": LangPidgin,

	"bawo": LangYoruba, "bawo ni": LangYoruba, "pele": LangYoruba, "e kaaro": LangYoruba, "e kaasan": LangYoruba,
	"e kaale": LangYoruba, "ekaaro": LangYoruba, "ekaasan": LangYoruba, "ekaale": LangYoruba,

	"kedu": LangIgbo, "ndewo": LangIgbo, "ututu oma": LangIgbo, "kedu ka i mere": LangIgbo, "kedu ka imere": LangIgbo,

	"sannu": LangHausa, "sannu da zuwa": LangHausa, "ina kwana": LangHausa, "barka da safe": LangHausa,
	"yaya dai": LangHausa, "ina wuni": LangHausa,
}

// greetingLanguage reports whether text is nothing but a greeting, and
// which language it's in. A message that merely starts with a greeting
// before going on to say something else ("hi, Chinedu paid me 5k") is
// deliberately not matched — the brief's "bare greeting" framing means
// the whole message, not a prefix, so a real financial message is never
// swallowed by this check.
func greetingLanguage(text string) (Language, bool) {
	normalized := normalizeGreeting(text)
	if normalized == "" {
		return "", false
	}
	lang, ok := greetingPhrases[normalized]
	return lang, ok
}

// normalizeGreeting lowercases, strips punctuation, and collapses
// whitespace — "Hi!!", "hi", and "  Hi  " all normalize the same way.
func normalizeGreeting(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if (r >= 'a' && r <= 'z') || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// Greeting menu button ids — dispatched deterministically in
// Processor.handleInteractive, never routed through the AI extractor.
const (
	menuCreateDebt = "menu:create_debt"
	menuBalance    = "menu:balance"
	menuHelp       = "menu:help"
)

// greetingText introduces Ruby and states what it can do in words, so
// the reply is still useful on a client that doesn't render the
// buttons (docs/BRIEF-interactive-messages.md: "don't just say 'Hello'
// back with nothing else").
var greetingText = map[Language]string{
	LangEnglish: "Hi! I'm Ruby, your bookkeeping assistant. I can record a debt, tell you who owes you, or explain what I can do.",
	LangPidgin:  "How far! Na Ruby be dis, your bookkeeping assistant. I fit record debt, tell you who still owe you, or explain wetin I fit do.",
	LangYoruba:  "Bawo! Ruby ni mi, olùrànlọ́wọ́ ìṣirò rẹ. Mo lè ṣe àkọsílẹ̀ gbèsè, sọ ẹni tó jẹ ọ́ ní gbèsè, tàbí ṣàlàyé ohun tí mo lè ṣe.",
	LangIgbo:    "Ndewo! Abụ m Ruby, onye enyemaka ndekọ gị. Enwere m ike ịdekọ ụgwọ, gwa gị onye ji gị ụgwọ, ma ọ bụ kọwaa ihe m nwere ike ime.",
	LangHausa:   "Sannu! Ni ne Ruby, mataimakiyar rikodin ka. Zan iya rikodin bashi, gaya maka wanda ke bin ka bashi, ko in bayyana abin da zan iya yi.",
}

// createDebtPromptText nudges the trader after they tap "Record a debt"
// from the greeting menu — English-only for now (see confirmationButtons'
// doc comment on why button-adjacent short strings don't need a
// translation table; this one's a full sentence, but the greeting menu
// itself doesn't carry per-tap language memory, a deliberately bounded
// simplification — the greeting reply is still fully localized, which is
// the required part).
const createDebtPromptText = `Sure — tell me who and how much, e.g. "Chinedu took 5k, pays Friday".`

func greetingMenuButtons() []Button {
	return []Button{
		{ID: menuCreateDebt, Title: "Record a debt"},
		{ID: menuBalance, Title: "Who owes me?"},
		{ID: menuHelp, Title: "Help"},
	}
}

func greetingReply(lang Language) Reply {
	return Reply{Text: fixedText(greetingText, lang), Buttons: greetingMenuButtons()}
}

// helpReply and unsupportedReply attach the same three quick-action
// buttons as the greeting menu (docs/BRIEF-polish-and-hardening.md #3:
// "for visual consistency every time this content appears, not just on
// first contact") to every path that shows the capability list — HELP,
// the honest decline for an unsupported request, and the greeting
// menu's own Help button (which reaches helpReply the same way).
func helpReply(lang Language) Reply {
	return Reply{Text: fixedText(helpText, lang), Buttons: greetingMenuButtons()}
}

func unsupportedReply(lang Language) Reply {
	return Reply{Text: fixedText(unsupportedRequestText, lang), Buttons: greetingMenuButtons()}
}

// returningGreetingReply is section 2's fix: a trader who already has a
// name (docs/BRIEF-response-quality.md #1/#2) gets a short, warm
// acknowledgment by name instead of the full intro every time — "it
// should feel like talking to an assistant that knows you." Buttons stay
// attached (the brief says they're optional here; keeping them is the
// simpler, more useful choice, per "if included, keep them but don't
// lead with the pitch" — the pitch is exactly what this text omits).
var returningGreetingText = map[Language]string{
	LangEnglish: "Hello %s! How can I help with your credit sales today?",
	LangPidgin:  "Hello %s! How I fit help you with your credit sales today?",
	LangYoruba:  "Bawo %s! Kín ni mo lè ràn ọ́ lọ́wọ́ pẹ̀lú ọjà rẹ tí a ta lórí gbèsè lónìí?",
	LangIgbo:    "Ndewo %s! Kedu ka m ga-esi nyere gị aka na ahịa gị ị na-ere n'ụgwọ taa?",
	LangHausa:   "Sannu %s! Ta yaya zan iya taimaka maka da tallace-tallacenka na bashi yau?",
}

func returningGreetingReply(lang Language, name string) Reply {
	return Reply{Text: fmt.Sprintf(fixedText(returningGreetingText, lang), name), Buttons: greetingMenuButtons()}
}

// nameQuestionText is appended to the first-contact intro (see
// firstContactReply) — "combined into the same message as the existing
// intro, not a separate follow-up" (docs/BRIEF-response-quality.md #1).
var nameQuestionText = map[Language]string{
	LangEnglish: "First — what should I call you?",
	LangPidgin:  "First — wetin I go dey call you?",
	LangYoruba:  "Àkọ́kọ́ — kí ni kí n máa pè ẹ́?",
	LangIgbo:    "Nke mbụ — gịnị ka m ga-akpọ gị?",
	LangHausa:   "Da farko — me zan kira ka?",
}

// firstContactReply is what a brand-new (nameless) account gets on its
// very first message: the same intro as greetingReply, plus the name
// question, in one message — never a separate follow-up turn.
func firstContactReply(lang Language) Reply {
	r := greetingReply(lang)
	r.Text = r.Text + " " + fixedText(nameQuestionText, lang)
	return r
}

// nameReaskText is the more direct re-ask when a reply "obviously isn't
// a name" (plan step 5) — English only: by the time Ruby is re-asking,
// it's already responding to a message that didn't look like a name, so
// there's no reliable language signal to key off beyond the (usually
// English) first-contact greeting already sent.
const nameReaskText = "I just need your name so I know what to call you — what should I put?"

// nameAcceptedText acknowledges a captured name (or the placeholder
// fallback) — %s is the name.
const nameAcceptedText = "Nice to meet you, %s!"

// namePlaceholder is used once a trader's second reply still doesn't
// look like a name (plan step 5): "stop trying... so the flow doesn't
// loop forever."
const namePlaceholder = "Trader"

// maxNameLength caps a stored name at a reasonable length — plan step
// 7: "reasonable length cap, no further validation gymnastics."
const maxNameLength = 60

func truncateName(name string) string {
	r := []rune(strings.TrimSpace(name))
	if len(r) <= maxNameLength {
		return string(r)
	}
	return string(r[:maxNameLength])
}

// looksLikeName is plan step 5's not-obviously-not-a-name check: reject
// anything digit-bearing (amounts, phone numbers, dates — "digits in a
// pattern that looks like a debt/payment message"), anything carrying a
// currency marker, and anything too long or multi-worded to plausibly
// be just a name. It is deliberately permissive otherwise — "a trader's
// name is whatever they say it is" (plan step 7); this only screens out
// the shape of a real financial request, not stylistic name choices.
func looksLikeName(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if len([]rune(trimmed)) > 40 {
		return false
	}
	if len(strings.Fields(trimmed)) > 4 {
		return false
	}
	for _, r := range trimmed {
		if r >= '0' && r <= '9' {
			return false
		}
	}
	lower := strings.ToLower(trimmed)
	for _, marker := range []string{"₦", "ngn", "naira", "kobo", "$"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}
