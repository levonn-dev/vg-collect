// Region tables: known regions, their free-text synonyms, and the
// chains and classifiers that resolve an entry region against
// catalog data and pricing listings.

package server

import (
	"strings"
)

// knownRegions is no longer a validation gate (region is
// open-world); it keys the machinery tables and the normalize
// lever's promotion target set.
var knownRegions = map[string]bool{
	"ntsc_u": true, "ntsc_j": true, "pal": true, "region_free": true,
	"korea": true, "brazil": true, "china": true,
}

// regionSynonyms maps each known region to the reviewed free-text
// forms the normalize lever promotes. Fold-matched (lowercase, trim),
// exact-or-synonym, never fuzzy: a string not listed here stays as
// typed. Graduating a region to knownRegions adds its row here (and
// in the enrichment twin - a stale twin costs an unpromoted string,
// never a wrong write).
var regionSynonyms = map[string][]string{
	"ntsc_u":      {"usa", "us", "ntsc", "ntsc-u", "north america"},
	"ntsc_j":      {"japan", "jp", "ntsc-j"},
	"pal":         {"europe", "eu"},
	"region_free": {"world", "worldwide", "region free"},
	"korea":       {"kr", "south korea"},
	"brazil":      {"br", "brasil"},
	"china":       {"cn"},
}

// regionFoldMap builds fold -> canonical from the known values'
// identity folds plus the synonyms rows.
func regionFoldMap() map[string]string {
	m := make(map[string]string, len(knownRegions)+8)
	for k := range knownRegions {
		m[k] = k
	}
	for canon, syns := range regionSynonyms {
		for _, s := range syns {
			m[s] = canon
		}
	}
	return m
}

// regionChains orders IGDB regions per entry region: the region's own
// territory first, its siblings, then worldwide. The language-market
// regions (korea, brazil, china) chain from their own entries only -
// their rows deliberately back no TV-standard region's chain, where
// they would reflect a localization launch, not the territory
// standard. korea and china share ntsc_j's asia sibling; brazil has
// no sibling market, so past its own row it falls to the scalar like
// any chain miss.
var regionChains = map[string][]string{
	"ntsc_u": {"north_america", "worldwide"},
	"ntsc_j": {"japan", "asia", "worldwide"},
	"pal":    {"europe", "australia", "new_zealand", "worldwide"},
	"korea":  {"korea", "asia", "worldwide"},
	"brazil": {"brazil", "worldwide"},
	"china":  {"china", "asia", "worldwide"},
}

// localizationChains orders IGDB localization regions per entry
// region; the first bundle present wins. Values are game_localizations
// region identifiers (ja-JP), NOT the release_region names (japan) -
// two provider vocabularies answering different questions, which is
// why this table stays separate from regionChains. ntsc_u and
// region_free have no chain: the canonical presentation is theirs.
// china prefers Simplified script and falls back to Traditional when
// only a zh-TW bundle exists.
var localizationChains = map[string][]string{
	"ntsc_j": {"ja-JP"},
	"pal":    {"EU"},
	"korea":  {"ko-KR"},
	"china":  {"zh-CN", "zh-TW"},
	"brazil": {"pt-BR"},
}

// jpConsoleNames are PriceCharting's distinct-name JP market consoles
// (the ones filed without a "jp " prefix). Sibling of enrichment's
// jpConsoleAliases values and the frontend's JP_CONSOLE_NAMES; a
// stale row here costs one no-op re-resolve, never a wrong repoint -
// the enrichment gate is authoritative.
var jpConsoleNames = map[string]bool{
	"famicom":             true,
	"super famicom":       true,
	"famicom disk system": true,
}

// consoleRegion classifies a PriceCharting console-name into the
// region class its listings price: "pal " prefix, "jp " prefix or a
// distinct JP market name, else base (the NA catalog and anything
// unknown).
func consoleRegion(consoleName string) string {
	c := strings.ToLower(strings.TrimSpace(consoleName))
	switch {
	case strings.HasPrefix(c, "pal "):
		return "pal"
	case strings.HasPrefix(c, "jp "), jpConsoleNames[c]:
		return "jp"
	default:
		return "base"
	}
}

// regionClass maps an entry region onto the console class that prices
// it; ntsc_u and region_free copies price from base listings, and so
// do korea, brazil and china - PriceCharting has no axes for those
// markets, so the base catalog is their deliberate pricing proxy.
func regionClass(region string) string {
	switch region {
	case "ntsc_j":
		return "jp"
	case "pal":
		return "pal"
	default:
		return "base"
	}
}
