package customer_test

import (
	"testing"
	"time"

	"github.com/chibuike-kt/ruby/internal/customer"
)

// Pure unit tests for the Hints tiebreaker logic (decisions.md #8) —
// no DB needed, since the function only operates on values the caller
// supplies. The end-to-end wiring through Resolve is covered by
// TestResolve_ByName_TrueWorstCase_FallsBackToCreationOrder in
// service_test.go.

func TestAmbiguousError_Hints_DescriptionsDistinguish(t *testing.T) {
	err := &customer.AmbiguousError{
		Candidates: []customer.Customer{
			{ID: 1, Name: "Chinedu"},
			{ID: 2, Name: "Chinedu"},
		},
	}

	hints := err.Hints(map[int64]string{
		1: "2 cartons of noodles",
		2: "1 bag of rice",
	})

	if hints[0].Hint != "2 cartons of noodles" {
		t.Fatalf("got hint %q, want the candidate's own debt description", hints[0].Hint)
	}
	if hints[1].Hint != "1 bag of rice" {
		t.Fatalf("got hint %q, want the candidate's own debt description", hints[1].Hint)
	}
}

func TestAmbiguousError_Hints_IdenticalDescriptions_FallsBackToCreationOrder(t *testing.T) {
	early := time.Date(2026, time.August, 2, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, time.August, 15, 9, 0, 0, 0, time.UTC)
	err := &customer.AmbiguousError{
		Candidates: []customer.Customer{
			{ID: 1, Name: "Chinedu", CreatedAt: early},
			{ID: 2, Name: "Chinedu", CreatedAt: late},
		},
	}

	// Same description on both — decisions.md #8 says this carries no
	// distinguishing power, unlike amount/due date it must never fall
	// back to using either of those instead.
	hints := err.Hints(map[int64]string{
		1: "2 cartons of noodles",
		2: "2 cartons of noodles",
	})

	if hints[0].Hint != "added 2 Aug 2026" {
		t.Fatalf("got hint %q, want a creation-order phrase for the earlier customer", hints[0].Hint)
	}
	if hints[1].Hint != "added 15 Aug 2026" {
		t.Fatalf("got hint %q, want a creation-order phrase for the later customer", hints[1].Hint)
	}
}

func TestAmbiguousError_Hints_NoDescriptionsSupplied_FallsBackToCreationOrder(t *testing.T) {
	when := time.Date(2026, time.August, 2, 9, 0, 0, 0, time.UTC)
	err := &customer.AmbiguousError{
		Candidates: []customer.Customer{
			{ID: 1, Name: "Chinedu", CreatedAt: when},
			{ID: 2, Name: "Chinedu", CreatedAt: when},
		},
	}

	hints := err.Hints(nil)

	for i, h := range hints {
		if h.Hint != "added 2 Aug 2026" {
			t.Fatalf("candidate %d: got hint %q, want the creation-order phrase", i, h.Hint)
		}
	}
}

func TestAmbiguousError_Hints_PartialDescriptions_StillDistinguish(t *testing.T) {
	err := &customer.AmbiguousError{
		Candidates: []customer.Customer{
			{ID: 1, Name: "Chinedu"},
			{ID: 2, Name: "Chinedu"},
		},
	}

	// Only candidate 1 has a debt with a description on record — that's
	// still a real difference between the two, not a tie.
	hints := err.Hints(map[int64]string{
		1: "2 cartons of noodles",
	})

	if hints[0].Hint != "2 cartons of noodles" {
		t.Fatalf("got hint %q, want the description", hints[0].Hint)
	}
	if hints[1].Hint != "" {
		t.Fatalf("got hint %q, want empty for a candidate with no description on record", hints[1].Hint)
	}
}
