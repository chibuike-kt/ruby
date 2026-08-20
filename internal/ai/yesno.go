package ai

// yesPhrases and noPhrases are docs/BRIEF-polish-and-hardening.md #3's
// free-text fallback for every yes/no style pending question (reminder
// opt-in, identity confirmation): "Every button-driven flow needs its
// free-text fallback to keep working exactly as before, buttons are
// additive, never a replacement for typing the answer." Deterministic,
// no AI call — same reasoning as cancelPhrases: a fixed list is a
// stronger guarantee than depending on the model to classify a
// one-word yes/no correctly every time, and it's the cheaper, faster
// path too.
var yesPhrases = map[string]bool{
	// English
	"yes": true, "yeah": true, "yep": true, "yup": true, "sure": true,
	"ok": true, "okay": true, "confirm": true, "correct": true, "right": true,
	"same": true, "same person": true,

	// Pidgin
	"oya": true, "na him": true, "na so": true, "abeg yes": true,

	// Yoruba — normalizeGreeting strips tone marks/diacritics (same as
	// greetingPhrases' own "bawo ni"/"e kaaro" entries), so keys here
	// are the plain-ASCII form a phone keyboard actually produces, not
	// the fully-accented spelling.
	"beeni": true, "bee ni": true,

	// Igbo
	"ee": true, "eh": true,

	// Hausa
	"i": true, "ii": true, "haka ne": true,
}

var noPhrases = map[string]bool{
	// English
	"no": true, "nope": true, "nah": true, "not now": true, "no thanks": true,
	"new": true, "different": true, "someone new": true, "not the same": true,

	// Pidgin
	"e no be am": true, "no be am": true, "na different person": true,

	// Yoruba
	"rara": true,

	// Igbo
	"mba": true,

	// Hausa
	"aa": true,
}

type yesNoAnswer int

const (
	answerUnclear yesNoAnswer = iota
	answerYes
	answerNo
)

// yesNoPhrase classifies text as a yes/no answer — the whole message,
// not a prefix or substring, same normalization as isCancelPhrase so a
// real message that merely contains "no" somewhere isn't swallowed by
// this check.
func yesNoPhrase(text string) yesNoAnswer {
	normalized := normalizeGreeting(text)
	if normalized == "" {
		return answerUnclear
	}
	if yesPhrases[normalized] {
		return answerYes
	}
	if noPhrases[normalized] {
		return answerNo
	}
	return answerUnclear
}
