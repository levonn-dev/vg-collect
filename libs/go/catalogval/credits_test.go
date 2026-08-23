package catalogval_test

// These cases pin the trim/drop-empty/nil-in-nil-out rules collection
// and enrichment both rely on. Cap enforcement (10-name maxItems,
// 120-char per-name maxLength) happens in the request validator
// (libs/go/specval, against each service's own contract), so
// NormalizeCredits never sees an over-cap list; no test case covers
// that here.

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
