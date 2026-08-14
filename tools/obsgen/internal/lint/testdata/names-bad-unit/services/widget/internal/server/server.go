// Package server is a deliberately broken names.Known fixture: a
// registration using a unit outside the exporter's four recognized
// forms ({x}, s, ms, By), proving Known fails loud on it instead of
// silently guessing a suffix.
package server

import vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"

func setup(m any) {
	weird, err := vgotel.Counter(m, "vg.widget.weird", "a counter with an unrecognized unit", "kg")
	if err != nil {
		panic(err)
	}
	_ = weird
}
