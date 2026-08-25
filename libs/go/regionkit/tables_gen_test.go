package regionkit_test

import (
	"testing"

	"github.com/levonn-dev/vgkeep/libs/go/regionkit"
)

// TestRegionClass_AllSevenRegions locks RegionClass's per-region class
// to api/domain.yaml's regions[].class for every declared region.
func TestRegionClass_AllSevenRegions(t *testing.T) {
	for region, want := range map[string]string{
		"ntsc_u":      "base",
		"ntsc_j":      "jp",
		"pal":         "pal",
		"korea":       "base",
		"brazil":      "base",
		"china":       "base",
		"region_free": "base",
	} {
		if got := regionkit.RegionClass(region); got != want {
			t.Fatalf("RegionClass(%q) = %q; want %q", region, got, want)
		}
	}
}

// TestRegionClass_UnknownRegionDefaultsToBase confirms an undeclared region falls through to
// "base", not empty or a panic.
func TestRegionClass_UnknownRegionDefaultsToBase(t *testing.T) {
	for _, region := range []string{"someday_region", "", "NTSC_U"} {
		if got := regionkit.RegionClass(region); got != "base" {
			t.Fatalf("RegionClass(%q) = %q; want %q", region, got, "base")
		}
	}
}

// TestConsoleRegion covers the "pal "/"jp " prefixes, each distinct-name JP console, the base
// fallback, and trim+lowercase normalization.
func TestConsoleRegion(t *testing.T) {
	for consoleName, want := range map[string]string{
		"Super Nintendo":       "base",
		"Playstation 4":        "base",
		"":                     "base",
		"Palworld Collection":  "base", // "pal" without the space suffix is not the PAL prefix
		"PAL Super Nintendo":   "pal",
		"PAL Xbox Series X":    "pal",
		"JP Sega Saturn":       "jp",
		"JP Nintendo Switch":   "jp",
		"Famicom":              "jp", // distinct-name JP console, no "jp " prefix
		"Super Famicom":        "jp",
		"Famicom Disk System":  "jp",
		"  Super Famicom  ":    "jp", // trim
		"FAMICOM":              "jp", // lowercase fold
		"  PAL Super Nintendo": "pal",
	} {
		if got := regionkit.ConsoleRegion(consoleName); got != want {
			t.Fatalf("ConsoleRegion(%q) = %q; want %q", consoleName, got, want)
		}
	}
}
