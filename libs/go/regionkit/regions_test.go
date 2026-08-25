package regionkit_test

import (
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/regionkit"
)

// TestKnownRegions_ExactlySevenRegions locks the known set to the reviewed seven.
func TestKnownRegions_ExactlySevenRegions(t *testing.T) {
	want := []string{"ntsc_u", "ntsc_j", "pal", "region_free", "korea", "brazil", "china"}
	if len(regionkit.KnownRegions) != len(want) {
		t.Fatalf("len(KnownRegions) = %d, want %d (%v)", len(regionkit.KnownRegions), len(want), want)
	}
	for _, r := range want {
		if !regionkit.KnownRegions[r] {
			t.Fatalf("KnownRegions missing %q", r)
		}
	}
}

// TestKnownRegions_MatchesGeneratedRegionNames pins that KnownRegions never drifts from the
// generated RegionNames table: same size, same members each way.
func TestKnownRegions_MatchesGeneratedRegionNames(t *testing.T) {
	fromNames := make(map[string]bool, len(regionkit.RegionNames))
	for _, r := range regionkit.RegionNames {
		fromNames[r] = true
	}
	if len(fromNames) != len(regionkit.KnownRegions) {
		t.Fatalf("RegionNames has %d distinct entries, KnownRegions has %d, want equal", len(fromNames), len(regionkit.KnownRegions))
	}
	for r := range fromNames {
		if !regionkit.KnownRegions[r] {
			t.Errorf("KnownRegions is missing %q, present in RegionNames", r)
		}
	}
	for r := range regionkit.KnownRegions {
		if !fromNames[r] {
			t.Errorf("KnownRegions has %q, not present in RegionNames", r)
		}
	}
}

// TestRegionFoldMap_EverySynonymFoldsToAKnownRegion confirms every synonym folds to a known canonical region.
func TestRegionFoldMap_EverySynonymFoldsToAKnownRegion(t *testing.T) {
	folds := regionkit.RegionFoldMap()
	for canon, syns := range regionkit.RegionSynonyms {
		if !regionkit.KnownRegions[canon] {
			t.Fatalf("RegionSynonyms key %q is not a known region", canon)
		}
		for _, s := range syns {
			if got := folds[s]; got != canon {
				t.Fatalf("RegionFoldMap[%q] = %q, want %q", s, got, canon)
			}
		}
	}
}

// TestRegionFoldMap_IdentityRowForEveryKnownRegion confirms a known region folds to itself.
func TestRegionFoldMap_IdentityRowForEveryKnownRegion(t *testing.T) {
	folds := regionkit.RegionFoldMap()
	for r := range regionkit.KnownRegions {
		if got := folds[r]; got != r {
			t.Fatalf("RegionFoldMap[%q] = %q, want identity %q", r, got, r)
		}
	}
}

// TestRegionFoldMap_NoSynonymCollidesWithADifferentRegionsIdentityFold guards against a
// synonym that is also another region's name, which would make the fold map ambiguous.
func TestRegionFoldMap_NoSynonymCollidesWithADifferentRegionsIdentityFold(t *testing.T) {
	for canon, syns := range regionkit.RegionSynonyms {
		for _, s := range syns {
			if regionkit.KnownRegions[s] && s != canon {
				t.Fatalf("synonym %q of %q collides with known region %q's identity fold", s, canon, s)
			}
		}
	}
}
