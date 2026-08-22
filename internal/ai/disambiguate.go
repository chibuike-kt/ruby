package ai

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/chibuike-kt/ruby/internal/customer"
)

// matchCandidate deterministically matches a trader's disambiguation
// reply against candidates, without an AI call — this is a selection
// step, not a language-understanding one. Order of matching:
//
//  1. A bare numeric index (1-based, in the order candidates were
//     presented).
//  2. A substring of any candidate's phone number.
//  3. A word that distinguishes one candidate's Hint from every other
//     candidate's (decisions.md #8: debt description, or "added <date>"
//     fallback) appearing in the reply.
//
// Returns ok=false if nothing matched confidently — the caller re-asks
// rather than guessing (spec §11).
func matchCandidate(reply string, candidates []PendingCandidateOption) (PendingCandidateOption, bool) {
	reply = strings.TrimSpace(reply)
	if reply == "" || len(candidates) == 0 {
		return PendingCandidateOption{}, false
	}

	if idx, err := strconv.Atoi(reply); err == nil {
		if idx >= 1 && idx <= len(candidates) {
			return candidates[idx-1], true
		}
		return PendingCandidateOption{}, false
	}

	for _, c := range candidates {
		if c.Phone != "" && phoneMatches(reply, c.Phone) {
			return c, true
		}
	}

	lowerReply := strings.ToLower(reply)
	distinguishing := distinguishingHintWords(candidates)

	var matches []PendingCandidateOption
	for _, c := range candidates {
		if anyWordIn(distinguishing[c.CustomerID], lowerReply) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return PendingCandidateOption{}, false
}

// distinguishingHintWords strips out words common to every candidate's
// Hint (e.g. "added"/the shared month in a creation-date fallback, per
// decisions.md #8) before matching — otherwise a reply like "the one
// added 15 aug" would match every August-added candidate on "added"/
// "aug" alone instead of the one word that actually distinguishes them,
// the day number. A candidate left with nothing distinguishing (hints
// that are fully identical) falls back to its own full word list, which
// then correctly fails to narrow to one match — decisions.md #8's true
// worst case, where matchCandidate must refuse to guess.
func distinguishingHintWords(candidates []PendingCandidateOption) map[int64][]string {
	perCandidate := make(map[int64][]string, len(candidates))
	counts := map[string]int{}
	for _, c := range candidates {
		words := hintTokens(c.Hint)
		perCandidate[c.CustomerID] = words
		seen := map[string]bool{}
		for _, w := range words {
			if !seen[w] {
				counts[w]++
				seen[w] = true
			}
		}
	}

	out := make(map[int64][]string, len(candidates))
	for id, words := range perCandidate {
		var unique []string
		for _, w := range words {
			if counts[w] < len(candidates) {
				unique = append(unique, w)
			}
		}
		if len(unique) == 0 {
			unique = words
		}
		out[id] = unique
	}
	return out
}

// hintTokens keeps words of 3+ letters, or any digit token (day/year
// numbers) — short digit tokens are exactly what usually distinguishes
// two "added <date>" fallback hints.
func hintTokens(hint string) []string {
	var out []string
	for word := range strings.FieldsSeq(strings.ToLower(hint)) {
		if len(word) < 3 && !isAllDigits(word) {
			continue
		}
		out = append(out, word)
	}
	return out
}

func anyWordIn(words []string, lowerReply string) bool {
	for _, w := range words {
		if strings.Contains(lowerReply, w) {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// phoneMatches compares digits only, against the phone's last 10 digits
// (a Nigerian local number's length) — so a trader typing the familiar
// local form ("0803...") still matches a phone stored in E.164
// ("+234803...").
func phoneMatches(reply, phone string) bool {
	phoneDigits := onlyDigits(phone)
	if phoneDigits == "" {
		return false
	}
	if len(phoneDigits) > 10 {
		phoneDigits = phoneDigits[len(phoneDigits)-10:]
	}
	return strings.Contains(onlyDigits(reply), phoneDigits)
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// matchCandidateByID finds the candidate an interactive button_reply or
// list_reply's id refers to. The id is the candidate's own customer_id
// (docs/BRIEF-interactive-messages.md), so this is a direct lookup, not
// matchCandidate's text-matching heuristics — "no need to re-run the
// deterministic text matching... since the id is the answer."
func matchCandidateByID(id int64, candidates []PendingCandidateOption) (PendingCandidateOption, bool) {
	for _, c := range candidates {
		if c.CustomerID == id {
			return c, true
		}
	}
	return PendingCandidateOption{}, false
}

// numberedTitle mirrors the numbering matchCandidate's numeric-index
// path already expects ("1", "2", ...) — every candidate in a real
// AmbiguousError shares the identical name (that's the whole reason
// disambiguation is needed), so the number is what actually
// distinguishes the buttons/list rows from each other.
func numberedTitle(index int, name string) string {
	return fmt.Sprintf("%d. %s", index+1, name)
}

// hintAliasLabelText, hintPhoneLabelText, and hintDescriptionLabelText
// label what kind of hint is being shown — docs/BRIEF-research-
// hardening-standard.md Part 5 live-testing finding #2: two candidates
// sharing an unlabeled hint format ("Chinedu — Atlas" vs "Chinedu — two
// cartons of noodles") reads as if "Atlas" were a purchased item, not a
// person-identifier, even though the underlying data was correct.
// HintCreatedDate needs no label of its own — hintFor's own "added <date>"
// phrasing is already self-explanatory prose, not a bare value.
var hintAliasLabelText = map[Language]string{
	LangEnglish: "alias: %s",
	LangPidgin:  "wetin dem dey call am: %s",
	LangYoruba:  "orúkọ àpèjúwe: %s",
	LangIgbo:    "aha ọzọ: %s",
	LangHausa:   "sunan laƙabi: %s",
}

var hintPhoneLabelText = map[Language]string{
	LangEnglish: "phone: %s",
	LangPidgin:  "phone: %s",
	LangYoruba:  "nọ́mbà fóònù: %s",
	LangIgbo:    "nọmba ekwentị: %s",
	LangHausa:   "lambar waya: %s",
}

var hintDescriptionLabelText = map[Language]string{
	LangEnglish: "last item: %s",
	LangPidgin:  "di last thing wey dem buy: %s",
	LangYoruba:  "ọjà tí ó kẹ́yìn: %s",
	LangIgbo:    "ihe ikpeazụ: %s",
	LangHausa:   "abu na ƙarshe: %s",
}

// labeledHint wraps a raw hint value with what kind of hint it is,
// except HintCreatedDate (already self-explanatory) and an unset Kind
// (an older/test-double PendingCandidateOption with no kind recorded)
// — both render the bare value, exactly as before this fix.
func labeledHint(kind customer.HintKind, hint string, lang Language) string {
	switch kind {
	case customer.HintAlias:
		return fmt.Sprintf(fixedText(hintAliasLabelText, lang), hint)
	case customer.HintPhone:
		return fmt.Sprintf(fixedText(hintPhoneLabelText, lang), hint)
	case customer.HintDescription:
		return fmt.Sprintf(fixedText(hintDescriptionLabelText, lang), hint)
	default:
		return hint
	}
}

// candidateDisplay builds the numbered "1. Chinedu (alias: Atlas)"
// lines used both in the phrased prompt text and reused verbatim on
// every re-ask, so a mistyped/unmatched reply sees exactly the same
// numbering as the original prompt.
func candidateDisplay(candidates []PendingCandidateOption, lang Language) []string {
	items := make([]string, len(candidates))
	for i, c := range candidates {
		items[i] = fmt.Sprintf("%d. %s (%s)", i+1, c.Name, labeledHint(c.HintKind, c.Hint, lang))
	}
	return items
}

// disambiguationPresentation picks buttons (<=3 candidates) or a list
// (4-10) — spec: "4+ candidates -> list message instead. This should be
// rare... but must not break when it happens." More than a list's
// 10-option cap returns no interactive payload at all (text-only
// fallback) rather than sending WhatsApp a request it would reject.
func disambiguationPresentation(candidates []PendingCandidateOption) ([]Button, *ListPayload) {
	switch {
	case len(candidates) == 0:
		return nil, nil
	case len(candidates) <= 3:
		buttons := make([]Button, len(candidates))
		for i, c := range candidates {
			buttons[i] = Button{ID: strconv.FormatInt(c.CustomerID, 10), Title: numberedTitle(i, c.Name)}
		}
		return buttons, nil
	case len(candidates) <= 10:
		options := make([]ListOption, len(candidates))
		for i, c := range candidates {
			options[i] = ListOption{ID: strconv.FormatInt(c.CustomerID, 10), Title: numberedTitle(i, c.Name), Description: c.Hint}
		}
		return nil, &ListPayload{ButtonLabel: "Select", Sections: []ListSection{{Title: "Candidates", Options: options}}}
	default:
		return nil, nil
	}
}

// disambiguationReplyFor attaches the right interactive presentation to
// a disambiguation prompt's text — shared by the initial prompt and
// every re-ask, so a mistyped/unmatched reply still gets the same
// tappable options back, not a text-only re-ask.
func disambiguationReplyFor(text string, candidates []PendingCandidateOption) Reply {
	buttons, list := disambiguationPresentation(candidates)
	return Reply{Text: text, Buttons: buttons, List: list}
}
