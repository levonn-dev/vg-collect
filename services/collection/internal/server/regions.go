// Region tables: regionChains, the collection-only chain ordering an entry's
// release-date pick by IGDB region. Tables shared with enrichment/frontend
// (localization chains, JP console names, classifiers) generate from
// api/domain.yaml into regionkit (libs/go/regionkit/tables_gen.go).

package server

// regionChains orders IGDB regions per entry region: own territory first,
// siblings, then worldwide. Language-market regions (korea, brazil, china)
// chain from their own entries only, never backing a TV-standard region's
// chain (that would reflect a localization launch, not the territory standard).
// korea/china share ntsc_j's asia sibling; brazil has none, falling to the scalar.
var regionChains = map[string][]string{
	"ntsc_u": {"north_america", "worldwide"},
	"ntsc_j": {"japan", "asia", "worldwide"},
	"pal":    {"europe", "australia", "new_zealand", "worldwide"},
	"korea":  {"korea", "asia", "worldwide"},
	"brazil": {"brazil", "worldwide"},
	"china":  {"china", "asia", "worldwide"},
}
