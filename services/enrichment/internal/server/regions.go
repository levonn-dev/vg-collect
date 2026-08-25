// Region tables: the chain that resolves a region's provider-facing
// query form. The known-region and synonym tables live in regionkit.

package server

// regionQueryChains maps an entry region to the localization
// identifiers carrying its provider-facing name form (PriceCharting
// names JP listings in romaji). Sibling of regionkit.LocalizationChains.
var regionQueryChains = map[string][]string{
	"ntsc_j": {"ja-JP"},
}
