// Package server is test-only fixture source for names.Known's tests: it
// mirrors the three real registration shapes found across services/ and
// libs/go/ (a direct vgotel call - user/auth/enrichment's shape; a
// same-order pass-through closure - social/collection/bff's shape; a
// direct OTel SDK call - libs/go/pgkit's, libs/go/valkeykit's, and
// services/auth's own pool/signing-key gauges), so Known's extractor is
// proven against all three instead of one or two invented shapes. It
// never compiles as part of any real package - every go tool skips a
// testdata/ directory - so its imports need not resolve to real modules.
package server

import (
	"go.opentelemetry.io/otel/metric"

	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
)

func setup(m any) {
	// Direct call: a counter with a curly-brace (semantic, not
	// measurement) unit.
	failOpen, err := vgotel.Counter(m, "vg.widget.cache.fail_open",
		"Cache operations that failed and were failed open", "{event}")
	if err != nil {
		panic(err)
	}
	_ = failOpen

	// Pass-through closure: one counter/histogram closure per meter,
	// called with literal args at every registration site.
	counter := func(name, desc, unit string) any {
		c, err := vgotel.Counter(m, name, desc, unit)
		if err != nil {
			panic(err)
		}
		return c
	}
	histogram := func(name, desc, unit string, buckets ...float64) any {
		h, err := vgotel.Histogram(m, name, desc, unit, buckets...)
		if err != nil {
			panic(err)
		}
		return h
	}

	spins := counter("vg.widget.spins.count", "Widget spin attempts by outcome", "{spin}")
	_ = spins

	refresh := histogram("vg.widget.refresh.duration",
		"Elapsed seconds per widget refresh", "s", vgotel.DurationBuckets...)
	_ = refresh

	// Not statically resolvable (a built, not literal, name) - must be
	// silently unrecognized rather than erroring; every real
	// registration in this repo passes its name as a literal.
	dynamicName := "vg.widget." + "dynamic"
	dyn, err := vgotel.Counter(m, dynamicName, "not statically resolvable", "{x}")
	if err != nil {
		panic(err)
	}
	_ = dyn

	// Direct OTel SDK call, not through the vgotel wrapper: an
	// observable gauge with a metric.WithUnit option, mirroring
	// libs/go/pgkit's and libs/go/valkeykit's own pool-connection-count
	// gauges - no _total/_bucket/etc suffix at all (gauges are not
	// monotonic, so the Prometheus exporter never adds one).
	poolGauge, err := m.Int64ObservableGauge("vg.widget.pool.connections",
		metric.WithDescription("Connections currently open"),
		metric.WithUnit("{connection}"))
	if err != nil {
		panic(err)
	}
	_ = poolGauge

	// Direct OTel SDK call with no metric.WithUnit option at all - the
	// no-unit case: the unit defaults to "" (no suffix), the same
	// treatment an explicit curly-brace unit gets, but exercised via
	// absence rather than an empty string literal.
	noUnitCounter, err := m.Int64Counter("vg.widget.no_unit.count",
		metric.WithDescription("Has no unit option at all"))
	if err != nil {
		panic(err)
	}
	_ = noUnitCounter

	// Direct OTel SDK call with a real (non-curly-brace) unit, mirroring
	// libs/go/pgkit's own acquire-wait histogram: proves the unit-suffix-
	// then-structural-suffix ordering on the direct path specifically
	// ("s" -> "_seconds" applied to the base name before the histogram's
	// three-way "_bucket"/"_count"/"_sum" split), not just via the
	// vgotel-closure path above (vg.widget.refresh.duration).
	poolWait, err := m.Float64Histogram("vg.widget.pool.wait",
		metric.WithDescription("Time spent waiting for a pool connection"),
		metric.WithUnit("s"))
	if err != nil {
		panic(err)
	}
	_ = poolWait
}
