package ai

import "testing"

func TestGreetingLanguage_BareGreetings(t *testing.T) {
	cases := []struct {
		text string
		lang Language
	}{
		{"hi", LangEnglish},
		{"Hi", LangEnglish},
		{"HELLO", LangEnglish},
		{"hello!", LangEnglish},
		{"  hi  ", LangEnglish},
		{"good morning", LangEnglish},
		{"how far", LangPidgin},
		{"how you dey", LangPidgin},
		{"bawo", LangYoruba},
		{"pele", LangYoruba},
		{"kedu", LangIgbo},
		{"ndewo", LangIgbo},
		{"sannu", LangHausa},
		{"ina kwana", LangHausa},
	}
	for _, tc := range cases {
		lang, ok := greetingLanguage(tc.text)
		if !ok {
			t.Errorf("greetingLanguage(%q): got ok=false, want a match in %s", tc.text, tc.lang)
			continue
		}
		if lang != tc.lang {
			t.Errorf("greetingLanguage(%q): got language %q, want %q", tc.text, lang, tc.lang)
		}
	}
}

// TestGreetingLanguage_RealMessageNeverMatches is the flip side of the
// requirement: a real financial message that happens to start with a
// greeting word must still reach the extractor normally — "bare
// greeting" means the whole message, not a prefix.
func TestGreetingLanguage_RealMessageNeverMatches(t *testing.T) {
	cases := []string{
		"hi, Chinedu paid me 5k",
		"hello Chinedu took 75k, pays Friday",
		"good morning, who owes me?",
		"how far, Chinedu don pay",
	}
	for _, text := range cases {
		if _, ok := greetingLanguage(text); ok {
			t.Errorf("greetingLanguage(%q): got ok=true, want false — this is a real message, not a bare greeting", text)
		}
	}
}

func TestGreetingLanguage_NotAGreeting(t *testing.T) {
	cases := []string{"", "   ", "Chinedu took 5k", "who owes me?", "yes", "1"}
	for _, text := range cases {
		if lang, ok := greetingLanguage(text); ok {
			t.Errorf("greetingLanguage(%q): got ok=true (lang=%q), want false", text, lang)
		}
	}
}

func TestGreetingReply_IntroducesRubyAndOffersMenu(t *testing.T) {
	for _, lang := range supportedLanguages {
		reply := greetingReply(lang)
		if reply.Text == "" {
			t.Errorf("greetingReply(%q): got empty text", lang)
		}
		if len(reply.Buttons) != 3 {
			t.Fatalf("greetingReply(%q): got %d buttons, want 3 (Record a debt / Who owes me? / Help)", lang, len(reply.Buttons))
		}
		ids := map[string]bool{}
		for _, b := range reply.Buttons {
			ids[b.ID] = true
			if b.Title == "" {
				t.Errorf("greetingReply(%q): button %q has an empty title", lang, b.ID)
			}
		}
		for _, want := range []string{menuCreateDebt, menuBalance, menuHelp} {
			if !ids[want] {
				t.Errorf("greetingReply(%q): missing button id %q", lang, want)
			}
		}
	}
}

func TestLooksLikeName_AcceptsPlausibleNames(t *testing.T) {
	cases := []string{"Chinedu", "Mama Ngozi", "Ade", "Chinedu Okafor", "Ngozi-Chukwu"}
	for _, text := range cases {
		if !looksLikeName(text) {
			t.Errorf("looksLikeName(%q) = false, want true", text)
		}
	}
}

func TestLooksLikeName_RejectsRequestShapedReplies(t *testing.T) {
	cases := []string{
		"",
		"   ",
		"5k",
		"Chinedu took 5k",
		"₦75,000",
		"NGN 75000",
		"Chinedu paid me 30k on Friday for the noodles order",
		"08031234567",
	}
	for _, text := range cases {
		if looksLikeName(text) {
			t.Errorf("looksLikeName(%q) = true, want false", text)
		}
	}
}

// TestLooksLikeName_RejectsCommandAndMenuPhrases is docs/BRIEF-
// disambiguation-reminders-statements.md Tier 0's adjacent finding: a
// trader replying with a normal command/menu word ("Help", "Who owes
// me?") while a name-capture question is pending must never have that
// silently stored as their name — this loose check previously accepted
// any short, digit-free, question-mark-free text, which is exactly
// what these command phrases are shaped like.
func TestLooksLikeName_RejectsCommandAndMenuPhrases(t *testing.T) {
	cases := []string{
		"Help", "help",
		"Who owes me?", "who owes me",
		"Menu", "Balance",
		"Cancel", "cancel", "never mind", "forget it",
		"Record a debt",
		"Confirm", "Edit",
	}
	for _, text := range cases {
		if looksLikeName(text) {
			t.Errorf("looksLikeName(%q) = true, want false — this is a command/menu phrase, not a name", text)
		}
	}
}

func TestTruncateName_CapsLength(t *testing.T) {
	long := ""
	for range 100 {
		long += "a"
	}
	got := truncateName(long)
	if len([]rune(got)) != maxNameLength {
		t.Fatalf("got length %d, want %d", len([]rune(got)), maxNameLength)
	}
}

func TestTruncateName_ShortNameUnchanged(t *testing.T) {
	if got := truncateName("  Chinedu  "); got != "Chinedu" {
		t.Fatalf("got %q, want trimmed \"Chinedu\"", got)
	}
}
