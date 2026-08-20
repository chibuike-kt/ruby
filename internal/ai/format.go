package ai

import (
	"fmt"
	"strings"
	"time"

	"github.com/chibuike-kt/ruby/internal/money"
)

// outstandingDebtLine is one debt in a LIST_OUTSTANDING_DEBTS reply —
// see formatOutstandingDebtsList. customerID (not just the name) is
// what grouping keys on — decisions.md #8's own duplicate-name case
// means two different customers can share a display name, and grouping
// by name alone would wrongly merge them.
type outstandingDebtLine struct {
	customerID       int64
	customerName     string
	outstandingMinor int64
	dueDate          *time.Time
}

// dueDateDisplayFormat matches decisions.md #8's own precedent (day-level
// precision, no time-of-day noise).
const dueDateDisplayFormat = "2 Jan"

// customerDebtGroup is one customer's debts, in the order
// groupDebtsByCustomer first encountered them.
type customerDebtGroup struct {
	customerID   int64
	customerName string
	debts        []outstandingDebtLine
}

// groupDebtsByCustomer collapses per-debt lines into one group per
// customer_id, preserving each group's position at its first debt's
// original (chronological) place in lines — docs/BRIEF-final-demo-
// fixes.md #4: a customer with several outstanding debts used to show
// up as that many separate, non-adjacent entries with no way to tell
// they belonged together.
func groupDebtsByCustomer(lines []outstandingDebtLine) []customerDebtGroup {
	var groups []customerDebtGroup
	index := make(map[int64]int, len(lines))
	for _, l := range lines {
		if i, ok := index[l.customerID]; ok {
			groups[i].debts = append(groups[i].debts, l)
			continue
		}
		index[l.customerID] = len(groups)
		groups = append(groups, customerDebtGroup{customerID: l.customerID, customerName: l.customerName, debts: []outstandingDebtLine{l}})
	}
	return groups
}

// formatOutstandingDebtsList builds the "genuinely readable list, not a
// wall of text" docs/BRIEF-response-quality.md #4 asks for: WhatsApp
// *bold* on the customer name once per customer (docs/BRIEF-final-
// demo-fixes.md #4 — grouped, every debt listed under its own
// customer, with a per-customer subtotal when there's more than one),
// a blank line between customers, and — when there's more than one
// debtor — a closing grand-total line. Entirely deterministic: no AI
// call, so nothing here can ever violate the isGrounded backstop
// (there's no Phraser call for it to violate).
func formatOutstandingDebtsList(lines []outstandingDebtLine, totalMinor int64, lang Language) string {
	groups := groupDebtsByCustomer(lines)

	var b strings.Builder
	for i, g := range groups {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "*%s*", g.customerName)
		var subtotal int64
		for _, l := range g.debts {
			b.WriteString("\n")
			b.WriteString(money.FormatNaira(l.outstandingMinor))
			if l.dueDate != nil {
				fmt.Fprintf(&b, " — %s %s", fixedText(dueLabelText, lang), l.dueDate.Format(dueDateDisplayFormat))
			}
			subtotal += l.outstandingMinor
		}
		if len(g.debts) > 1 {
			fmt.Fprintf(&b, "\n%s %s", fixedText(subtotalLabelText, lang), money.FormatNaira(subtotal))
		}
	}
	if len(groups) > 1 {
		fmt.Fprintf(&b, "\n\n%s *%s*", fixedText(totalOutstandingLabelText, lang), money.FormatNaira(totalMinor))
	}
	return b.String()
}

// statementPaymentLine is one payment shown under its debt in a
// GET_CUSTOMER_STATEMENT reply.
type statementPaymentLine struct {
	amountMinor int64
	date        time.Time
}

// statementDebtLine is one debt (any status, unlike
// outstandingDebtLine) in a customer statement.
type statementDebtLine struct {
	description string
	amountMinor int64
	date        time.Time
	payments    []statementPaymentLine
}

// formatCustomerStatement builds docs/BRIEF-disambiguation-reminders-
// statements.md Tier 3's GET_CUSTOMER_STATEMENT reply deterministically
// — every debt with its item description and date, every payment
// against each, and the current outstanding balance — same reasoning
// as formatOutstandingDebtsList: entirely in Go, so nothing here can
// violate the isGrounded backstop.
func formatCustomerStatement(customerName string, lines []statementDebtLine, outstandingMinor int64, lang Language) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", fmt.Sprintf(fixedText(statementHeaderText, lang), customerName))
	for i, l := range lines {
		if i > 0 {
			b.WriteString("\n\n")
		}
		// docs/BRIEF-final-demo-fixes.md #5: a missing description just
		// doesn't render that piece — never a placeholder word standing
		// in for it.
		if l.description != "" {
			fmt.Fprintf(&b, "*%s* — ", l.description)
		}
		fmt.Fprintf(&b, "%s (%s)", money.FormatNaira(l.amountMinor), l.date.Format(dueDateDisplayFormat))
		if len(l.payments) == 0 {
			fmt.Fprintf(&b, "\n%s", fixedText(statementNoPaymentsText, lang))
			continue
		}
		for _, p := range l.payments {
			fmt.Fprintf(&b, "\n%s %s (%s)", fixedText(statementPaidLabelText, lang), money.FormatNaira(p.amountMinor), p.date.Format(dueDateDisplayFormat))
		}
	}
	fmt.Fprintf(&b, "\n\n%s *%s*", fixedText(statementOutstandingLabelText, lang), money.FormatNaira(outstandingMinor))
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
