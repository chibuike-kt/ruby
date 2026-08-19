package ai

import "testing"

func TestMatchCandidate_NumericIndex(t *testing.T) {
	candidates := []PendingCandidateOption{
		{CustomerID: 1, Phone: "+2348030000001", Hint: "2 cartons of noodles"},
		{CustomerID: 2, Phone: "+2348030000002", Hint: "1 bag of rice"},
	}

	got, ok := matchCandidate("2", candidates)
	if !ok || got.CustomerID != 2 {
		t.Fatalf("got %+v ok=%v, want candidate 2 (1-based index)", got, ok)
	}
}

func TestMatchCandidate_NumericIndex_OutOfRange(t *testing.T) {
	candidates := []PendingCandidateOption{{CustomerID: 1, Hint: "a"}, {CustomerID: 2, Hint: "b"}}

	if _, ok := matchCandidate("5", candidates); ok {
		t.Fatal("got a match for an out-of-range index, want no match")
	}
}

func TestMatchCandidate_PhoneSubstring(t *testing.T) {
	candidates := []PendingCandidateOption{
		{CustomerID: 1, Phone: "+2348030000001", Hint: "2 cartons of noodles"},
		{CustomerID: 2, Phone: "+2348030000002", Hint: "1 bag of rice"},
	}

	got, ok := matchCandidate("the one with 08030000002", candidates)
	if !ok || got.CustomerID != 2 {
		t.Fatalf("got %+v ok=%v, want candidate 2 (phone substring)", got, ok)
	}
}

func TestMatchCandidate_HintWordSubstring(t *testing.T) {
	candidates := []PendingCandidateOption{
		{CustomerID: 1, Hint: "2 cartons of noodles"},
		{CustomerID: 2, Hint: "1 bag of rice"},
	}

	got, ok := matchCandidate("the noodles one", candidates)
	if !ok || got.CustomerID != 1 {
		t.Fatalf("got %+v ok=%v, want candidate 1 (hint word match)", got, ok)
	}
}

func TestMatchCandidate_CreationDateFallback(t *testing.T) {
	candidates := []PendingCandidateOption{
		{CustomerID: 1, Hint: "added 2 Aug 2026"},
		{CustomerID: 2, Hint: "added 15 Aug 2026"},
	}

	got, ok := matchCandidate("the one added 15 aug", candidates)
	if !ok || got.CustomerID != 2 {
		t.Fatalf("got %+v ok=%v, want candidate 2", got, ok)
	}
}

func TestMatchCandidate_NoMatch(t *testing.T) {
	candidates := []PendingCandidateOption{
		{CustomerID: 1, Phone: "+2348030000001", Hint: "2 cartons of noodles"},
		{CustomerID: 2, Phone: "+2348030000002", Hint: "1 bag of rice"},
	}

	if _, ok := matchCandidate("I don't know, whichever", candidates); ok {
		t.Fatal("got a match for an unrelated reply, want no match")
	}
}

func TestMatchCandidate_IdenticalHints_NoMatch(t *testing.T) {
	candidates := []PendingCandidateOption{
		{CustomerID: 1, Hint: "2 cartons of noodles"},
		{CustomerID: 2, Hint: "2 cartons of noodles"},
	}

	// decisions.md #8's true worst case: nothing distinguishes the two
	// hints at all — must not guess (spec §11).
	if _, ok := matchCandidate("the noodles one", candidates); ok {
		t.Fatal("got a match when both candidates have identical hints, want no match")
	}
}

func TestMatchCandidate_EmptyReply(t *testing.T) {
	candidates := []PendingCandidateOption{{CustomerID: 1, Hint: "a"}}
	if _, ok := matchCandidate("   ", candidates); ok {
		t.Fatal("got a match for a blank reply, want no match")
	}
}

func TestMatchCandidate_NoCandidates(t *testing.T) {
	if _, ok := matchCandidate("1", nil); ok {
		t.Fatal("got a match with no candidates, want no match")
	}
}
