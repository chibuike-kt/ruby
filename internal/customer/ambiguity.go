package customer

// HintKind names what kind of value CandidateHint.Hint actually is — an
// alias is a person-identifier a trader chose specifically to tell
// same-named customers apart, while a description is whatever item they
// last bought. The two read as plausible sentences either way ("Chinedu
// — Atlas" vs "Chinedu — two cartons of noodles"), so a candidate list
// mixing an alias for one match and a description for another must
// label which is which — a bare hint string with no kind attached is
// exactly how "Atlas" gets misread as a purchased item instead of a
// person's nickname (docs/BRIEF-research-hardening-standard.md Part 5
// live-testing finding #2).
type HintKind string

const (
	HintAlias       HintKind = "alias"
	HintPhone       HintKind = "phone"
	HintDescription HintKind = "description"
	HintCreatedDate HintKind = "created_date"
)

// CandidateHint pairs an ambiguous match with the text Ruby should show
// a trader to help them pick between candidates (spec §11) — display
// enrichment only, never used to auto-select a candidate.
type CandidateHint struct {
	Customer Customer
	Hint     string
	Kind     HintKind
}

// hintDateFormat matches decisions.md #8's own example ("added 2 Aug
// 2026") — day-level precision is enough for a trader to recall roughly
// when they added someone; time-of-day would just be noise.
const hintDateFormat = "2 Jan 2006"

// Hints builds a distinguishing label per candidate (decisions.md #8,
// extended by docs/BRIEF-disambiguation-reminders-statements.md Tier
// 1c): never force the trader to recall their own past alias choice —
// show real distinguishing detail outright, in descending order of how
// reliably it actually identifies the person:
//
//  1. The candidate's own alias, if one was set at creation — the
//     strongest signal, since a trader who bothered to set one
//     specifically meant to tell same-named customers apart.
//  2. The candidate's own phone number, if one is on file.
//  3. If the descriptions supplied for these candidates aren't all
//     identical, each candidate's own description — a real signal
//     ("2 cartons of noodles" vs "1 bag of rice"). Debt amount and due
//     date are never used this way: two debts matching on amount and
//     date is coincidence, not identity, and treating it as a signal
//     is exactly the kind of guess spec §11 forbids.
//  4. Otherwise fall back to creation order, phrased for a human
//     rather than a raw customer_id. id and created_at exist
//     unconditionally on every row and always differ between two
//     distinct rows, so this never runs out of distinguishing power —
//     even when name, alias, phone, amount, due date, and description
//     are all identical.
//
// descriptions is keyed by customer id, one entry per candidate — the
// most recent outstanding transaction's item description, per Tier 1c
// ("the most recent transaction's item description"). internal/customer
// has no debt data of its own — internal/debt already imports customer
// for ownership checks, so the reverse import would cycle — so
// whichever layer holds both customer and debt context supplies
// descriptions here rather than this package fetching them itself. A
// nil or incomplete map just means that candidate falls through to a
// later step in the hierarchy.
func (e *AmbiguousError) Hints(descriptions map[int64]string) []CandidateHint {
	distinguishing := len(distinctValues(e.Candidates, descriptions)) > 1

	hints := make([]CandidateHint, len(e.Candidates))
	for i, c := range e.Candidates {
		value, kind := hintFor(c, descriptions[c.ID], distinguishing)
		hints[i] = CandidateHint{Customer: c, Hint: value, Kind: kind}
	}
	return hints
}

func hintFor(c Customer, description string, descriptionDistinguishes bool) (string, HintKind) {
	if alias := trimmed(c.Alias); alias != "" {
		return alias, HintAlias
	}
	if phone := trimmed(c.PhoneNumber); phone != "" {
		return phone, HintPhone
	}
	if descriptionDistinguishes {
		// description itself may be "" for this specific candidate —
		// that's still a real contrast against a sibling that has one,
		// not a tie, so it's used as-is rather than falling further.
		return description, HintDescription
	}
	return "added " + c.CreatedAt.Format(hintDateFormat), HintCreatedDate
}

func distinctValues(candidates []Customer, descriptions map[int64]string) map[string]bool {
	seen := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		seen[descriptions[c.ID]] = true
	}
	return seen
}
