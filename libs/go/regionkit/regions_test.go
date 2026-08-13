package regionkit_test

import (
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/regionkit"
)

// TestKnownRegions_ExactlySevenRegions locks the known set to the
// reviewed seven; a graduation or a typo both show up as a length or
// membership mismatch here.
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

// TestRegionFoldMap_EverySynonymFoldsToAKnownRegion confirms every
// RegionSynonyms row folds to a canonical key that is itself known,
// and that RegionFoldMap actually wires the row up.
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

// TestRegionFoldMap_IdentityRowForEveryKnownRegion confirms a known
// region folds to itself, so an already-canonical value never misses
// the fold map.
func TestRegionFoldMap_IdentityRowForEveryKnownRegion(t *testing.T) {
	folds := regionkit.RegionFoldMap()
	for r := range regionkit.KnownRegions {
		if got := folds[r]; got != r {
			t.Fatalf("RegionFoldMap[%q] = %q, want identity %q", r, got, r)
		}
	}
}

// TestRegionFoldMap_NoSynonymCollidesWithADifferentRegionsIdentityFold
// guards against a synonym string that is also a known region name
// belonging to some other region: that would make the fold map
// ambiguous between the identity row and the synonym row.
func TestRegionFoldMap_NoSynonymCollidesWithADifferentRegionsIdentityFold(t *testing.T) {
	for canon, syns := range regionkit.RegionSynonyms {
		for _, s := range syns {
			if regionkit.KnownRegions[s] && s != canon {
				t.Fatalf("synonym %q of %q collides with known region %q's identity fold", s, canon, s)
			}
		}
	}
}
