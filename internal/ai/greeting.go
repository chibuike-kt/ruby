package ai

import "strings"

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

func greetingReply(lang Language) Reply {
	return Reply{
		Text: fixedText(greetingText, lang),
		Buttons: []Button{
			{ID: menuCreateDebt, Title: "Record a debt"},
			{ID: menuBalance, Title: "Who owes me?"},
			{ID: menuHelp, Title: "Help"},
		},
	}
}
