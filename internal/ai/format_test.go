package ai

import "testing"

func TestFormatNaira(t *testing.T) {
	cases := []struct {
		minor int64
		want  string
	}{
		{0, "₦0"},
		{100, "₦1"},
		{7_500_000, "₦75,000"},
		{1_234_567_00, "₦1,234,567"},
		{150, "₦1.50"},
		{-7_500_000, "-₦75,000"},
	}
	for _, tc := range cases {
		if got := formatNaira(tc.minor); got != tc.want {
			t.Errorf("formatNaira(%d) = %q, want %q", tc.minor, got, tc.want)
		}
	}
}

func TestGroupThousands(t *testing.T) {
	cases := map[string]string{
		"0":         "0",
		"75":        "75",
		"750":       "750",
		"7500":      "7,500",
		"75000":     "75,000",
		"1234567":   "1,234,567",
		"123456789": "123,456,789",
	}
	for in, want := range cases {
		if got := groupThousands(in); got != want {
			t.Errorf("groupThousands(%q) = %q, want %q", in, got, want)
		}
	}
}
