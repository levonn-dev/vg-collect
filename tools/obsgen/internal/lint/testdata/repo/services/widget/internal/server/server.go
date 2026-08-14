// Package server is fixture-only source read by lint.Run's tests
// (through Run's own internal Known(repoRoot) call, the same path
// production lint takes) - it registers the one metric this fixture
// tree's rules and panels reference.
package server

import (
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
)

func setup(m any) {
	spins, err := vgotel.Counter(m, "vg.widget.spins.count", "Widget spin attempts by outcome", "{spin}")
	if err != nil {
		panic(err)
	}
	_ = spins
}
