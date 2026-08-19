package ai

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// outstandingDebtLine is one entry in a LIST_OUTSTANDING_DEBTS reply —
// see formatOutstandingDebtsList.
type outstandingDebtLine struct {
	customerName     string
	outstandingMinor int64
	dueDate          *time.Time
}

// dueDateDisplayFormat matches decisions.md #8's own precedent (day-level
// precision, no time-of-day noise).
const dueDateDisplayFormat = "2 Jan"

// formatOutstandingDebtsList builds the "genuinely readable list, not a
// wall of text" docs/BRIEF-response-quality.md #4 asks for: WhatsApp
// *bold* on the customer name, amount (and due date, if set) on the
// next line, a blank line between entries, and — when there's more than
// one debtor — a closing total line. Entirely deterministic: no AI call,
// so nothing here can ever violate the isGrounded backstop (there's no
// Phraser call for it to violate).
func formatOutstandingDebtsList(lines []outstandingDebtLine, totalMinor int64, lang Language) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "*%s*\n%s", l.customerName, formatNaira(l.outstandingMinor))
		if l.dueDate != nil {
			fmt.Fprintf(&b, " — %s %s", fixedText(dueLabelText, lang), l.dueDate.Format(dueDateDisplayFormat))
		}
	}
	if len(lines) > 1 {
		fmt.Fprintf(&b, "\n\n%s *%s*", fixedText(totalOutstandingLabelText, lang), formatNaira(totalMinor))
	}
	return b.String()
}

// formatNaira renders minor units as "₦75,000" (or "₦75,000.50" for a
// kobo remainder) — display-only, mirrors money.Money.String()'s own
// "not for parsing back" caveat but with the thousands separators a
// trader-facing message actually needs.
func formatNaira(minor int64) string {
	negative := minor < 0
	if negative {
		minor = -minor
	}
	naira, kobo := minor/100, minor%100

	s := "₦" + groupThousands(strconv.FormatInt(naira, 10))
	if kobo != 0 {
		s += fmt.Sprintf(".%02d", kobo)
	}
	if negative {
		s = "-" + s
	}
	return s
}

func groupThousands(digits string) string {
	n := len(digits)
	if n <= 3 {
		return digits
	}
	firstGroup := n % 3
	if firstGroup == 0 {
		firstGroup = 3
	}

	var b strings.Builder
	b.WriteString(digits[:firstGroup])
	for i := firstGroup; i < n; i += 3 {
		b.WriteByte(',')
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}
