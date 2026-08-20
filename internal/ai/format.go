package ai

import (
	"fmt"
	"strings"
	"time"

	"github.com/chibuike-kt/ruby/internal/money"
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
		fmt.Fprintf(&b, "*%s*\n%s", l.customerName, money.FormatNaira(l.outstandingMinor))
		if l.dueDate != nil {
			fmt.Fprintf(&b, " — %s %s", fixedText(dueLabelText, lang), l.dueDate.Format(dueDateDisplayFormat))
		}
	}
	if len(lines) > 1 {
		fmt.Fprintf(&b, "\n\n%s *%s*", fixedText(totalOutstandingLabelText, lang), money.FormatNaira(totalMinor))
	}
	return b.String()
}

// formatCustomerList builds LIST_CUSTOMERS' reply deterministically
// (docs/BRIEF-critical-fixes-and-reminders.md #2a) — the same reasoning
// as formatOutstandingDebtsList: a list of real names is exactly the
// kind of content a Phraser call could drop or hallucinate around,
// especially the empty case (an empty/omitted Items field left the
// model to improvise a "stub response with no content"). Deterministic
// formatting sidesteps that entirely — no Phraser call, so nothing here
// can violate isGrounded either.
func formatCustomerList(names []string, lang Language) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", fixedText(customerListHeaderText, lang))
	for i, name := range names {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- *%s*", name)
	}
	return b.String()
}
