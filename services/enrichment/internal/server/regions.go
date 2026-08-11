// Region tables: known regions, their free-text synonyms, and the
// chains that resolve a region's provider-facing query form.

package server

// regionQueryChains maps an entry region to the localization
// identifiers whose bundles carry the provider-facing name form for
// that region's listings (PriceCharting names JP listings in romaji).
// The enrichment-side sibling of collection's localizationChains,
// answering a provider-query question rather than a display one.
var regionQueryChains = map[string][]string{
	"ntsc_j": {"ja-JP"},
}

// knownRegions is enrichment's twin of collection's knownRegions
// (services/collection/internal/server/regions.go): it keys the
// normalize-community-regions promotion target set. Not a validation
// gate here either - community.region stays open-world, same as
// collection's entry region.
var knownRegions = map[string]bool{
	"ntsc_u": true, "ntsc_j": true, "pal": true, "region_free": true,
	"korea": true, "brazil": true, "china": true,
}

// regionSynonyms is enrichment's twin of collection's regionSynonyms,
// row-identical by construction: the same localization-chains twin
// posture regionQueryChains documents above - a stale twin costs an
// unpromoted community.region string, never a wrong write, so this
// table is reviewed alongside its sibling rather than derived from
// it. Fold-matched (lowercase, trim), exact-or-synonym, never fuzzy:
// a string not listed here stays as typed. Graduating a region to
// knownRegions adds its row here AND in the collection sibling.
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
// identity folds plus the synonyms rows (enrichment's twin of
// collection's regionFoldMap).
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
