// Package expand holds golden-content expansion shared by the alert,
// dashboard, and lint pipelines: service-name substitution and block
// instantiation.
package expand

import (
	"maps"
	"slices"
	"strings"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// Substitute replaces {Service}/{service} placeholders (capitalized and
// lowercase forms) with service, wherever they appear in s - an alert
// template's uid, title, expr, summary, or panel_ref, or a dashboard
// golden panel fragment's title, expr, or label selector. Order between
// the two replacements does not matter: the two placeholder spellings
// never overlap as substrings of each other.
func Substitute(s, service string) string {
	s = strings.ReplaceAll(s, "{Service}", DisplayName(service))
	s = strings.ReplaceAll(s, "{service}", service)
	return s
}

// serviceDisplayNames overrides capitalize's plain first-letter
// fallback for a service whose {Service} form is not just its name
// capitalized: bff is an acronym, not a plain word, so it must render
// "BFF", not "Bff" - the owner ruling this table exists for. Add an
// entry here only when capitalize's fallback is wrong for that service
// - every other service's {Service} form still comes from capitalize
// alone.
var serviceDisplayNames = map[string]string{
	"bff": "BFF",
}

// DisplayName resolves service's {Service} form: serviceDisplayNames'
// override if one exists, else capitalize's plain fallback.
func DisplayName(service string) string {
	if d, ok := serviceDisplayNames[service]; ok {
		return d
	}
	return capitalize(service)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// BlockPanel is one golden block panel instantiated for one service:
// {service}/{Service} already substituted into Fragment, paired with
// the y anchor that service's own golden_blocks entry gave Block.
// Fragment's own gridPos.y is left exactly as authored - block-relative,
// an offset from AnchorY - Blocks never touches gridPos; adding the two
// together is internal/dashboards.Assemble's job.
type BlockPanel struct {
	Service  string
	Block    string
	AnchorY  int
	Fragment string
}

// Blocks instantiates every golden block each service's manifest opts
// into (golden_blocks), once per panel, substituting {service}/
// {Service} into the panel's fragment text. A service with no
// golden_blocks entries is skipped outright - a service is never
// required to use a block. Order is deterministic: service (m.
// Dashboards.Services' own order, which Load guarantees is
// alphabetical), then that service's block names alphabetically
// (ServiceDash.GoldenBlocks is a Go map), then panel order within the
// block (Block.Panels' own declaration order).
func Blocks(m *manifest.Model) []BlockPanel {
	var out []BlockPanel
	for _, sd := range m.Dashboards.Services {
		if len(sd.GoldenBlocks) == 0 {
			continue
		}

		for _, name := range slices.Sorted(maps.Keys(sd.GoldenBlocks)) {
			anchor := sd.GoldenBlocks[name]
			for _, fragment := range m.Dashboards.Blocks[name].Panels {
				out = append(out, BlockPanel{
					Service:  sd.Service,
					Block:    name,
					AnchorY:  anchor,
					Fragment: Substitute(fragment, sd.Service),
				})
			}
		}
	}
	return out
}
