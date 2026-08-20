package ai

import "fmt"

// Reminder opt-in button ids (docs/BRIEF-fixes-and-reminders.md #4) —
// fixed, English-only, same reasoning as confirmationButtons.
const (
	buttonReminderYes = "reminder:yes"
	buttonReminderNo  = "reminder:no"
)

func reminderOptInButtons() []Button {
	return []Button{
		{ID: buttonReminderYes, Title: "Yes, remind them"},
		{ID: buttonReminderNo, Title: "No thanks"},
	}
}

// reminderOptInText is the brief's own example verbatim: "Want me to
// remind [customer name] before this is due?" — appended to the
// DEBT_CREATED confirmation, not a separate follow-up turn (same
// "combined into one message" reasoning as firstContactReply).
var reminderOptInText = map[Language]string{
	LangEnglish: "Want me to remind *%s* before this is due?",
	LangPidgin:  "You want make I remind *%s* before e reach due date?",
	LangYoruba:  "Ṣé o fẹ́ kí n rán *%s* létí kí ọjọ́ náà tó dé?",
	LangIgbo:    "Ị chọrọ ka m cheta *%s* tupu ụbọchị a ruo?",
	LangHausa:   "Kana son in tunatar da *%s* kafin ranar biya ta zo?",
}

func reminderOptInSuffix(lang Language, customerName string) string {
	return fmt.Sprintf(fixedText(reminderOptInText, lang), customerName)
}

// reminderPhoneRequestText is decisions.md-style: the customer has no
// phone number on file, so before Ruby can schedule anything it needs
// one (docs/BRIEF-fixes-and-reminders.md #4's "Yes, and the customer has
// no phone number on file -> ask for it now").
var reminderPhoneRequestText = map[Language]string{
	LangEnglish: "What's their phone number, so I can send the reminder?",
	LangPidgin:  "Wetin be dia phone number, make I fit send the reminder?",
	LangYoruba:  "Kí ni nọ́mbà fóònù wọn, kí n lè fi ìránnilétí náà ránṣẹ́?",
	LangIgbo:    "Kedu nọmba ekwentị ha, ka m nwee ike izipu ncheta ahụ?",
	LangHausa:   "Menene lambar wayarsu, don in aika tunatarwar?",
}

// reminderPhoneReaskText is the more direct re-ask when a reply doesn't
// look like a phone number at all (empty, or nothing digit-bearing) —
// mirrors onboarding.go's nameReaskText/customerSignalReaskText.
const reminderPhoneReaskText = "I just need a phone number to send the reminder to — what should I use?"

// reminderScheduledText confirms scheduling — %s is the customer name.
var reminderScheduledText = map[Language]string{
	LangEnglish: "Got it — I'll remind *%s* the day before, and again on the due date.",
	LangPidgin:  "Okay — I go remind *%s* the day before, and again on the due date.",
	LangYoruba:  "Ó dára — màá rán *%s* létí ní ọjọ́ kan ṣáájú, àti lọ́jọ́ tí ó yẹ kó san.",
	LangIgbo:    "Ọ dị mma — m ga-echeta *%s* otu ụbọchị tupu, na ọzọ n'ụbọchị a kwesịrị ịkwụ ụgwọ.",
	LangHausa:   "Na gane — zan tunatar da *%s* rana guda kafin, kuma a ranar biyan.",
}

// reminderDeclinedText acknowledges "No thanks" — a brief neutral
// acknowledgment rather than dead silence after a button tap, matching
// every other interactive reply in this file (editPromptText,
// cancelledText).
var reminderDeclinedText = map[Language]string{
	LangEnglish: "No problem!",
	LangPidgin:  "No wahala!",
	LangYoruba:  "Kò sí ìṣòro!",
	LangIgbo:    "Ọ dịghị nsogbu!",
	LangHausa:   "Babu matsala!",
}
