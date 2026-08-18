package customer

// CandidateHint pairs an ambiguous match with the text Ruby should show
// a trader to help them pick between candidates (spec §11) — display
// enrichment only, never used to auto-select a candidate.
type CandidateHint struct {
	Customer Customer
	Hint     string
}

// hintDateFormat matches decisions.md #8's own example ("added 2 Aug
// 2026") — day-level precision is enough for a trader to recall roughly
// when they added someone; time-of-day would just be noise.
const hintDateFormat = "2 Jan 2006"

// Hints builds a distinguishing label per candidate (decisions.md #8):
//
//  1. If the descriptions supplied for these candidates aren't all
//     identical, use each candidate's own description — a real signal
//     ("2 cartons of noodles" vs "1 bag of rice"). Debt amount and due
//     date are never used this way: two debts matching on amount and
//     date is coincidence, not identity, and treating it as a signal
//     is exactly the kind of guess spec §11 forbids.
//  2. Otherwise fall back to creation order, phrased for a human
//     rather than a raw customer_id. id and created_at exist
//     unconditionally on every row and always differ between two
//     distinct rows, so this never runs out of distinguishing power —
//     even when name, amount, due date, and description are all
//     identical.
//
// descriptions is keyed by customer id. internal/customer has no debt
// data of its own — internal/debt already imports customer for
// ownership checks, so the reverse import would cycle — so whichever
// layer holds both customer and debt context supplies descriptions
// here rather than this package fetching them itself. A nil or
// incomplete map just means every candidate falls back to case 2.
func (e *AmbiguousError) Hints(descriptions map[int64]string) []CandidateHint {
	distinguishing := len(distinctValues(e.Candidates, descriptions)) > 1

	hints := make([]CandidateHint, len(e.Candidates))
	for i, c := range e.Candidates {
		hint := descriptions[c.ID]
		if !distinguishing {
			hint = "added " + c.CreatedAt.Format(hintDateFormat)
		}
		hints[i] = CandidateHint{Customer: c, Hint: hint}
	}
	return hints
}

func distinctValues(candidates []Customer, descriptions map[int64]string) map[string]bool {
	seen := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		seen[descriptions[c.ID]] = true
	}
	return seen
}
