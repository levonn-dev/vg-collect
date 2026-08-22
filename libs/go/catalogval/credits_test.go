package catalogval_test

// TDD for NormalizeCredits: lifted from collection's normalizeCredits
// and enrichment's normalizeCommunityCredits, which were byte-identical
// twins before this lib existed. These cases pin the trim/drop-empty/
// nil-in-nil-out rules both call sites relied on. The cap rules
// (10-name maxItems, 120-char per-name maxLength) NormalizeCredits
// used to enforce moved to the request validator (libs/go/specval,
// against each service's own contract); this package no longer sees
// an over-cap list at all; specval rejects it upstream.

import (
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/catalogval"
)

func TestNormalizeCredits_NilInNilOut(t *testing.T) {
	out := catalogval.NormalizeCredits(nil)
	if out != nil {
		t.Fatalf("got %v, want nil", out)
	}
}

func TestNormalizeCredits_EmptySliceYieldsNil(t *testing.T) {
	names := []string{}
	out := catalogval.NormalizeCredits(&names)
	if out != nil {
		t.Fatalf("got %v, want nil", out)
	}
}

func TestNormalizeCredits_AllBlankYieldsNil(t *testing.T) {
	names := []string{"  ", "\t", ""}
	out := catalogval.NormalizeCredits(&names)
	if out != nil {
		t.Fatalf("got %v, want nil", out)
	}
}

// TestNormalizeCredits_TrimsAndDropsBlankInterior pins the two rules
// together: each surviving name is trimmed to just its own content
// (no internal whitespace touched, only the leading/trailing kind),
// and a blank element anywhere in the list - not just at the ends -
// drops rather than surviving as "".
func TestNormalizeCredits_TrimsAndDropsBlankInterior(t *testing.T) {
	names := []string{"  Nintendo ", "", "  ", "Square Enix"}
	out := catalogval.NormalizeCredits(&names)
	want := []string{"Nintendo", "Square Enix"}
	if len(out) != len(want) || out[0] != want[0] || out[1] != want[1] {
		t.Fatalf("out = %v, want %v", out, want)
	}
}
