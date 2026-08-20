package ai

import "testing"

// TestExtractName covers docs/BRIEF-fixes-and-reminders.md #1's required
// cases: common self-introduction patterns strip down to the actual
// name, and the bare case (no self-intro at all) passes through
// unchanged rather than being mangled.
func TestExtractName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"i'm", "I'm Kingsley", "Kingsley"},
		{"i am", "I am Kingsley", "Kingsley"},
		{"my name is", "My name is Kingsley", "Kingsley"},
		{"call me", "Call me Kingsley", "Kingsley"},
		{"this is", "This is Kingsley", "Kingsley"},
		{"bare", "Kingsley", "Kingsley"},
		{"bare with space", "  Kingsley  ", "Kingsley"},
		{"trailing punctuation", "I'm Kingsley.", "Kingsley"},

		// Pidgin
		{"pidgin na", "Na Kingsley", "Kingsley"},
		{"pidgin i be", "I be Kingsley", "Kingsley"},
		{"pidgin my name na", "My name na Kingsley", "Kingsley"},
		{"pidgin dem call me", "Dem call me Kingsley", "Kingsley"},
		{"pidgin dem dey call me", "Dem dey call me Kingsley", "Kingsley"},

		// Yoruba
		{"yoruba oruko mi ni", "Oruko mi ni Kingsley", "Kingsley"},
		{"yoruba emi ni", "Emi ni Kingsley", "Kingsley"},

		// Igbo
		{"igbo aha m bu", "Aha m bu Kingsley", "Kingsley"},
		{"igbo abu m", "Abu m Kingsley", "Kingsley"},

		// Hausa
		{"hausa sunana", "Sunana Kingsley", "Kingsley"},
		{"hausa suna na", "Suna na Kingsley", "Kingsley"},
		{"hausa ni ne", "Ni ne Kingsley", "Kingsley"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractName(tc.in)
			if got != tc.want {
				t.Fatalf("extractName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestExtractName_DoesNotLoosenRejection confirms extraction never
// smuggles a request-shaped reply past looksLikeName — the brief is
// explicit that this fix is about extracting from a valid name-shaped
// reply, not loosening what counts as one.
func TestExtractName_DoesNotLoosenRejection(t *testing.T) {
	cases := []string{
		"Chinedu took 5k",
		"I'm sending 5k to Chinedu",
		"My name is not the point, Chinedu owes me 5k",
	}
	for _, in := range cases {
		extracted := extractName(in)
		if looksLikeName(extracted) {
			t.Fatalf("extractName(%q) = %q, looksLikeName should still reject this", in, extracted)
		}
	}
}
