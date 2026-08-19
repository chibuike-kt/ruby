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
