package ai

import "testing"

func TestIsGrounded_AcceptsAmountsActuallyGiven(t *testing.T) {
	amount := int64(7500000) // ₦75,000
	input := PhraseInput{Event: EventDebtCreated, AmountMinor: &amount, OutstandingMinor: &amount}

	cases := []string{
		"Debt recorded! Chinedu owes NGN 75,000.",
		"Debt recorded! Chinedu owes NGN 7500000 kobo.",
		"Outstanding balance: NGN 75,000.00.",
	}
	for _, text := range cases {
		if !isGrounded(text, input) {
			t.Fatalf("got not-grounded for %q, want grounded — every number it states is actually in the input", text)
		}
	}
}

// TestIsGrounded_RejectsZeroWhenFieldWasNeverZero is the regression case
// for the reported bug directly: even though "0" is a very ordinary
// number, it must never be treated as automatically safe just because
// it's small — the whole point is that the model previously stated a
// ₦0 outstanding balance when the real value was ₦75,000, and a naive
// "always allow 0 for decimal-formatting artifacts" rule would let that
// exact failure straight through undetected.
func TestIsGrounded_RejectsZeroWhenFieldWasNeverZero(t *testing.T) {
	amount := int64(7500000)
	input := PhraseInput{Event: EventDebtCreated, AmountMinor: &amount, OutstandingMinor: &amount}

	if isGrounded("Debt recorded! Chinedu owes NGN 75,000. Outstanding balance: NGN 0.", input) {
		t.Fatal("got grounded for a reply stating an outstanding balance of 0 when outstanding_minor was actually 7500000 — this must be rejected")
	}
}

// TestIsGrounded_AcceptsGenuineZero proves the check above isn't just a
// blanket ban on "0": a debt that's genuinely fully settled has a real
// outstanding_minor of 0, and stating that must still be allowed.
func TestIsGrounded_AcceptsGenuineZero(t *testing.T) {
	amount := int64(7500000)
	zero := int64(0)
	input := PhraseInput{Event: EventPaymentRecorded, AmountMinor: &amount, OutstandingMinor: &zero}

	if !isGrounded("Payment recorded! Chinedu now owes NGN 0 — fully paid.", input) {
		t.Fatal("got not-grounded for a reply stating a genuinely-zero outstanding balance, want grounded")
	}
}

func TestIsGrounded_RejectsInventedAmount(t *testing.T) {
	amount := int64(7500000)
	input := PhraseInput{Event: EventDebtCreated, AmountMinor: &amount, OutstandingMinor: &amount}

	if isGrounded("Debt recorded! Chinedu owes NGN 120,000.", input) {
		t.Fatal("got grounded for a reply stating an amount that never appeared in the input")
	}
}

func TestIsGrounded_OmittedFieldMeansNumberIsUngrounded(t *testing.T) {
	// EventConfirmationNeeded with no due date: DueDateISO is "", so any
	// date-shaped number the model states must be rejected — it wasn't
	// given one.
	amount := int64(7500000)
	input := PhraseInput{Event: EventConfirmationNeeded, CustomerName: "Chinedu", AmountMinor: &amount}

	if isGrounded("Should I record this for Chinedu, due on the 25th?", input) {
		t.Fatal("got grounded for a due date the model was never given (DueDateISO was empty)")
	}
}

func TestIsGrounded_AllowsDateComponentsFromInput(t *testing.T) {
	amount := int64(7500000)
	input := PhraseInput{Event: EventDebtCreated, AmountMinor: &amount, OutstandingMinor: &amount, DueDateISO: "2026-08-21"}

	if !isGrounded("Debt recorded for NGN 75,000, due 21 August 2026.", input) {
		t.Fatal("got not-grounded for a reply using the day/year straight from DueDateISO")
	}
}

func TestIsGrounded_AllowsDigitsFromDescriptionAndItems(t *testing.T) {
	input := PhraseInput{Event: EventCustomerList, Items: []string{"Chinedu", "Ngozi"}, Description: "2 cartons of noodles"}

	if !isGrounded("You have 2 customers: Chinedu and Ngozi.", input) {
		t.Fatal("got not-grounded for a number that was already present in Description, want grounded")
	}
}

func TestIsGrounded_NoNumbersInTextAlwaysGrounded(t *testing.T) {
	input := PhraseInput{Event: EventCustomerNotFound, Language: LangEnglish, CustomerName: "Chinedu"}
	if !isGrounded("I don't know who Chinedu is yet.", input) {
		t.Fatal("got not-grounded for text with no digits at all")
	}
}
