// Region tables: the chain that resolves a region's provider-facing
// query form. The known-region and synonym tables live in regionkit.

package server

// regionQueryChains maps an entry region to the localization
// identifiers whose bundles carry the provider-facing name form for
// that region's listings (PriceCharting names JP listings in romaji).
// The enrichment-side sibling of the generated
// regionkit.LocalizationChains, answering a provider-query question
// rather than a display one.
var regionQueryChains = map[string][]string{
	"ntsc_j": {"ja-JP"},
}
