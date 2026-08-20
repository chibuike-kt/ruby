package ai

import (
	"fmt"
	"strings"
)

// Identity-confirmation button ids (docs/BRIEF-fixes-and-reminders.md
// #3, decisions.md #9) — fixed, English-only, same reasoning as
// confirmationButtons: short generic labels read fine in every
// supported language.
const (
	buttonIdentitySame = "identity:same"
	buttonIdentityNew  = "identity:new"
)

func identityConfirmationButtons() []Button {
	return []Button{
		{ID: buttonIdentitySame, Title: "Same person"},
		{ID: buttonIdentityNew, Title: "Someone new"},
	}
}

// identityConfirmationText is decisions.md #9's own example verbatim:
// "You already have a customer named Chinedu. Is this the same Chinedu,
// or someone new?" %s appears twice — the name.
var identityConfirmationText = map[Language]string{
	LangEnglish: "You already have a customer named *%[1]s*. Is this the same %[1]s, or someone new?",
	LangPidgin:  "You don already get customer wey dem call *%[1]s*. Na dis same %[1]s, or na different person?",
	LangYoruba:  "O ti ní oníbàárà kan tí orúkọ rẹ̀ ń jẹ́ *%[1]s*. Ṣé èyí ni %[1]s kan náà, tàbí ẹlòmíràn ni?",
	LangIgbo:    "Ị nweelarị onye ahịa aha ya bụ *%[1]s*. Ọ bụ otu %[1]s ahụ, ka ọ bụ onye ọzọ?",
	LangHausa:   "Ka riga ka da abokin ciniki mai suna *%[1]s*. Wannan %[1]s ɗin ne, ko wani ne daban?",
}

func identityConfirmationReply(lang Language, name string) Reply {
	return Reply{Text: fmt.Sprintf(fixedText(identityConfirmationText, lang), name), Buttons: identityConfirmationButtons()}
}

// customerSignalRequestText is decisions.md #8's creation guard, phrased
// as a question instead of a rejection — the trader just said this is a
// *different* person from the existing %s, so Ruby needs a phone number
// or alias before creating a same-named record (docs/BRIEF-fixes-and-
// reminders.md #3's "New person" branch).
var customerSignalRequestText = map[Language]string{
	LangEnglish: "Got it — what's their phone number or a short alias, so I don't mix them up with the existing *%s*?",
	LangPidgin:  "Okay — wetin be dia phone number or one short alias, make I no mix dem up with the existing *%s*?",
	LangYoruba:  "Ó dára — kí ni nọ́mbà fóònù wọn tàbí orúkọ ìrọ̀rùn kan, kí n má bàa dàrú wọn pẹ̀lú *%s* tó ti wà?",
	LangIgbo:    "Ọ dị mma — kedu nọmba ekwentị ha ma ọ bụ obere aha ọzọ, ka m ghara ijikọ ha na *%s* dị ugbu a?",
	LangHausa:   "To — menene lambar wayarsu ko wani suna gajere, don kada in rikita su da *%s* da ke akwai?",
}

func customerSignalRequestReply(lang Language, existingName string) Reply {
	return textReply(fmt.Sprintf(fixedText(customerSignalRequestText, lang), existingName))
}

// customerSignalReaskText is the more direct re-ask when a reply to
// customerSignalRequestReply is empty/unusable — mirrors
// onboarding.go's nameReaskText, same reasoning: by this point Ruby is
// already responding to a reply that didn't work, so there's no
// reliable language signal left to key off.
const customerSignalReaskText = "I just need a phone number or a short alias to tell them apart — what should I use?"

// looksLikePhone reports whether text is mostly digits once common
// phone-number punctuation (spaces, dashes, parens, a leading +) is
// stripped — decisions.md #8 only needs "phone number or alias," not
// strict E.164 validation, so this is a shape check, not a format
// validator. Anything that doesn't look like a phone number is treated
// as an alias instead (customerSignalReply below).
func looksLikePhone(text string) bool {
	digits := 0
	for _, r := range strings.TrimSpace(text) {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == ' ' || r == '-' || r == '(' || r == ')' || r == '+':
			continue
		default:
			return false
		}
	}
	return digits >= 7
}
