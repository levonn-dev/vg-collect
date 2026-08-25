// Package expand holds golden-content expansion shared by the alert,
// dashboard, and lint pipelines: service-name substitution and block instantiation.
package expand

import (
	"maps"
	"slices"
	"strings"

	"github.com/levonn-dev/vgkeep/tools/obsgen/internal/manifest"
)

// Substitute replaces {Service}/{service} placeholders with service,
// wherever they appear in s (the two spellings never overlap as substrings).
func Substitute(s, service string) string {
	s = strings.ReplaceAll(s, "{Service}", DisplayName(service))
	s = strings.ReplaceAll(s, "{service}", service)
	return s
}

// serviceDisplayNames overrides capitalize's fallback for a service
// whose {Service} form isn't plain capitalization (bff -> "BFF", not "Bff").
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
// Fragment already has {service}/{Service} substituted; its gridPos.y is
// left block-relative (an offset from AnchorY) - Assemble adds the two.
type BlockPanel struct {
	Service  string
	Block    string
	AnchorY  int
	Fragment string
}

// Blocks instantiates every golden block each service opts into
// (golden_blocks), substituting {service}/{Service} per panel. Order is
// deterministic: service, then block name (GoldenBlocks is a Go map,
// sorted), then panel declaration order.
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
