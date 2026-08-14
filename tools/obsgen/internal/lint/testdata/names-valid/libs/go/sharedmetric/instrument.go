// Package sharedmetric is test-only fixture source for names.Known's
// tests, proving the scan also covers libs/go/ (not just services/): a
// direct histogram with an "ms" unit and a direct counter with a "By"
// unit - the two exporter suffix rules the services/widget fixture does
// not exercise.
package sharedmetric

import (
	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
)

func setup(m any) {
	lag, err := vgotel.Histogram(m, "vg.shared.queue.lag", "Queue lag", "ms")
	if err != nil {
		panic(err)
	}
	_ = lag

	payload, err := vgotel.Counter(m, "vg.shared.payload.size", "Payload bytes seen", "By")
	if err != nil {
		panic(err)
	}
	_ = payload
}
