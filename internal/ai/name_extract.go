package ai

import (
	"regexp"
	"strings"
)

// namePrefixPatterns matches common self-introduction phrasings across
// Ruby's 5 supported languages (docs/BRIEF-fixes-and-reminders.md #1) —
// "I'm Kingsley" must store "Kingsley", not the sentence verbatim. Each
// pattern captures everything after the introductory phrase; the bare
// case ("Kingsley" alone) matches nothing and falls through unchanged.
var namePrefixPatterns = []*regexp.Regexp{
	// English
	regexp.MustCompile(`(?i)^i'?m\s+(.+)$`),
	regexp.MustCompile(`(?i)^i\s+am\s+(.+)$`),
	regexp.MustCompile(`(?i)^my\s+name\s+is\s+(.+)$`),
	regexp.MustCompile(`(?i)^call\s+me\s+(.+)$`),
	regexp.MustCompile(`(?i)^this\s+is\s+(.+)$`),

	// Pidgin — "my name na X", "dem dey call me X" first, since they're
	// more specific than the bare "na X" / "i be X" self-intro.
	regexp.MustCompile(`(?i)^my\s+name\s+na\s+(.+)$`),
	regexp.MustCompile(`(?i)^dem\s+(?:dey\s+)?call\s+me\s+(.+)$`),
	regexp.MustCompile(`(?i)^na\s+(.+)$`),
	regexp.MustCompile(`(?i)^i\s+be\s+(.+)$`),

	// Yoruba — "oruko mi ni X" (my name is X), "emi ni X" (I am X).
	// Accepts both the diacritic and plain-ASCII spellings traders
	// actually type on a phone keyboard.
	regexp.MustCompile(`(?i)^or[uú]k[oọ]\s+mi\s+ni\s+(.+)$`),
	regexp.MustCompile(`(?i)^emi\s+ni\s+(.+)$`),

	// Igbo — "aha m bu X" (my name is X), "abu m X" (I am X).
	regexp.MustCompile(`(?i)^aha\s+m\s+b[uụ]\s+(.+)$`),
	regexp.MustCompile(`(?i)^ab[uụ]\s+m\s+(.+)$`),

	// Hausa — "sunana X" / "suna na X" (my name is X), "ni ne X" (I am X).
	regexp.MustCompile(`(?i)^suna\s*na\s+(.+)$`),
	regexp.MustCompile(`(?i)^ni\s+ne\s+(.+)$`),
}

// extractName pulls the actual name out of a self-introduction reply —
// "I'm Kingsley" -> "Kingsley", "My name is Kingsley" -> "Kingsley" —
// before it's stored. This only extracts; it never loosens what counts
// as a valid reply — looksLikeName still runs on the result exactly as
// before. A reply that matches no known pattern is returned unchanged,
// which is what keeps the bare case ("Kingsley" alone) working.
func extractName(text string) string {
	trimmed := strings.TrimSpace(text)
	for _, pattern := range namePrefixPatterns {
		m := pattern.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		candidate := strings.TrimSpace(m[1])
		candidate = strings.TrimRight(candidate, ".!, ")
		if candidate != "" {
			return candidate
		}
	}
	return trimmed
}
