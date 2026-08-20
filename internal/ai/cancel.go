package ai

// cancelPhrases maps a normalized "I want out" phrase to nothing in
// particular — presence in the set is the whole signal. Deterministic,
// no AI call, same reasoning as greetingPhrases: a fixed list is a
// stronger guarantee than depending on the model to recognize "the
// trader wants to abandon this" correctly every time, and it's cheaper.
//
// Edge case #4 (docs/BRIEF-critical-fixes-and-reminders.md's
// interactive slot-filling section): "never mind," "cancel," "forget
// it," and equivalents across all 5 languages must cleanly exit *any*
// pending state — not implemented per-flow, so this is checked once, in
// process(), before dispatching to whichever pending kind is active.
var cancelPhrases = map[string]bool{
	// English
	"cancel": true, "never mind": true, "nevermind": true, "nvm": true,
	"forget it": true, "forget that": true, "forget about it": true,
	"stop": true, "leave it": true, "not interested": true,

	// Pidgin
	"forget am": true, "abeg forget am": true, "make e stay": true,
	"no mind am": true, "leave am": true, "i no mind again": true,
	"comot am": true,

	// Yoruba
	"gbagbe e": true, "fi sile": true, "mi o nifee mo": true,
	"ma gbagbe e": true,

	// Igbo
	"chefuo ya": true, "hapu ya": true, "achoghim ya ozo": true,

	// Hausa
	"manta da shi": true, "bari": true, "ba na so kuma": true,
	"ka daina": true,
}

// isCancelPhrase reports whether text is nothing but an "I want out"
// signal — reuses normalizeGreeting's exact normalization (lowercase,
// strip punctuation, collapse whitespace) since the matching need is
// identical: the whole message, not a prefix, so a real message that
// merely contains the word "stop" somewhere isn't swallowed by this.
func isCancelPhrase(text string) bool {
	normalized := normalizeGreeting(text)
	if normalized == "" {
		return false
	}
	return cancelPhrases[normalized]
}

// stickyOrPendingLanguage picks a language to reply in when a pending
// action is cancelled — the pending intent's own language if it has
// one, otherwise English (mirrors nameReaskText's own reasoning: by
// the time something is pending, there's usually already a language
// signal to reuse).
func stickyOrPendingLanguage(pending PendingAction) Language {
	if pending.Intent.Language != "" {
		return pending.Intent.Language
	}
	return LangEnglish
}
