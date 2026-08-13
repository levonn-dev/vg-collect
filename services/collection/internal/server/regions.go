// Region tables: regionChains, the collection-only chain that orders
// an entry's release-date pick by IGDB region. The tables shared with
// enrichment and the frontend (localization chains, JP console names,
// the console/region classifiers) generate from api/domain.yaml into
// regionkit; see libs/go/regionkit/tables_gen.go. The known-region and
// synonym tables live in regionkit too.

package server

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
