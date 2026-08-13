package catalogval_test

// TDD for NormalizeCredits: lifted from collection's normalizeCredits
// and enrichment's normalizeCommunityCredits, which were byte-identical
// twins before this lib existed. These cases pin the exact rules both
// call sites relied on (trim, drop-empty, the 120-char rune cap, the
// 10-name cap, and nil-in/nil-out) so the shared function cannot drift
// from either twin's prior behavior.

import (
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/catalogval"
)

func TestNormalizeCredits_NilInNilOut(t *testing.T) {
	out, detail := catalogval.NormalizeCredits("developers", nil)
	if out != nil || detail != "" {
		t.Fatalf("got (%v, %q), want (nil, \"\")", out, detail)
	}
}

func TestNormalizeCredits_EmptySliceYieldsNil(t *testing.T) {
	names := []string{}
	out, detail := catalogval.NormalizeCredits("developers", &names)
	if out != nil || detail != "" {
		t.Fatalf("got (%v, %q), want (nil, \"\")", out, detail)
	}
}

func TestNormalizeCredits_AllBlankYieldsNil(t *testing.T) {
	names := []string{"  ", "\t", ""}
	out, detail := catalogval.NormalizeCredits("developers", &names)
	if out != nil || detail != "" {
		t.Fatalf("got (%v, %q), want (nil, \"\")", out, detail)
	}
}

func TestNormalizeCredits_TrimsAndDropsBlankInterior(t *testing.T) {
	names := []string{"  Nintendo ", "", "  ", "Capcom"}
	out, detail := catalogval.NormalizeCredits("publishers", &names)
	if detail != "" {
		t.Fatalf("detail = %q, want none", detail)
	}
	want := []string{"Nintendo", "Capcom"}
	if len(out) != len(want) || out[0] != want[0] || out[1] != want[1] {
		t.Fatalf("out = %v, want %v", out, want)
	}
}

func TestNormalizeCredits_120RuneNamePasses(t *testing.T) {
	// A 120-rune name built from a 2-byte-per-rune character (240
	// bytes, 120 runes): passing pins that the cap counts runes, not
	// bytes - a byte-length check would have wrongly rejected this.
	name := strings.Repeat("n", 120)
	names := []string{name}
	out, detail := catalogval.NormalizeCredits("developers", &names)
	if detail != "" {
		t.Fatalf("detail = %q, want none (120 runes is the boundary, not over it)", detail)
	}
	if len(out) != 1 || out[0] != name {
		t.Fatalf("out = %v, want [%q]", out, name)
	}
}

func TestNormalizeCredits_121RuneNameFails(t *testing.T) {
	name := strings.Repeat("n", 121)
	names := []string{name}
	out, detail := catalogval.NormalizeCredits("developers", &names)
	if out != nil {
		t.Fatalf("out = %v, want nil on a length rejection", out)
	}
	if detail != "developers names must be at most 120 characters" {
		t.Fatalf("detail = %q, want the field-specific length message", detail)
	}
}

func TestNormalizeCredits_MultiByteRuneCountedNotBytes(t *testing.T) {
	// 120 copies of a 2-byte rune: 240 bytes but 120 runes. Must pass -
	// same boundary as the ASCII case, proving the cap is rune-based.
	name := strings.Repeat("ñ", 120) // "n with tilde", 2 UTF-8 bytes each
	names := []string{name}
	out, detail := catalogval.NormalizeCredits("developers", &names)
	if detail != "" {
		t.Fatalf("detail = %q, want none: 120 runes must pass even at 240 bytes", detail)
	}
	if len(out) != 1 {
		t.Fatalf("out = %v, want the one name kept", out)
	}
}

func TestNormalizeCredits_TenNamesPass(t *testing.T) {
	names := make([]string, 10)
	for i := range names {
		names[i] = "name"
	}
	out, detail := catalogval.NormalizeCredits("developers", &names)
	if detail != "" {
		t.Fatalf("detail = %q, want none (10 is the boundary, not over it)", detail)
	}
	if len(out) != 10 {
		t.Fatalf("len(out) = %d, want 10", len(out))
	}
}

func TestNormalizeCredits_ElevenNamesFail(t *testing.T) {
	names := make([]string, 11)
	for i := range names {
		names[i] = "name"
	}
	out, detail := catalogval.NormalizeCredits("publishers", &names)
	if out != nil {
		t.Fatalf("out = %v, want nil on a count rejection", out)
	}
	if detail != "publishers must list at most 10 names" {
		t.Fatalf("detail = %q, want the field-specific count message", detail)
	}
}

func TestNormalizeCredits_ElevenRawWithBlanksCanStillPass(t *testing.T) {
	// 11 raw elements, but one is blank and drops out before the count
	// cap is evaluated: the cap applies to the post-trim count, not the
	// input length.
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "  "}
	out, detail := catalogval.NormalizeCredits("developers", &names)
	if detail != "" {
		t.Fatalf("detail = %q, want none: the blank must not count toward the 10 cap", detail)
	}
	if len(out) != 10 {
		t.Fatalf("len(out) = %d, want 10", len(out))
	}
}

func TestNormalizeCredits_LengthRejectionShortCircuitsBeforeCountCheck(t *testing.T) {
	// Only 3 raw elements (well under the 10 cap), but the second is
	// over the 120-rune cap: the length check must fire on that
	// element rather than the loop completing and passing the (unmet)
	// count check.
	names := []string{"ok", strings.Repeat("x", 121), "also ok"}
	out, detail := catalogval.NormalizeCredits("developers", &names)
	if out != nil {
		t.Fatalf("out = %v, want nil", out)
	}
	if detail != "developers names must be at most 120 characters" {
		t.Fatalf("detail = %q, want the length message even though the list is short", detail)
	}
}
