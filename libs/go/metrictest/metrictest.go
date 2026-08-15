// Package metrictest installs an OpenTelemetry ManualReader over the
// global MeterProvider for a test and hands back typed helpers for
// reading what it collects. Five call sites across auth, collection,
// enrichment, and user each hand-rolled this same install under three
// different cleanup semantics; this package settles on restoring the
// exact prior provider (shutting down the one it installed) - the
// careful behavior enrichment and user already used - as the one
// every adopter now gets.
package metrictest

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Install swaps the global meter provider for a fresh SDK provider
// draining into the returned ManualReader. Cleanup restores the exact
// prior provider and shuts down the one this call installed.
//
// Resetting to a fresh blank provider or a noop provider (the two
// other semantics this package replaces) both discard whatever the
// global pointed at before the test ran. That is safe only when
// nothing else in the same test binary depends on it still being
// there - which does not hold for a package whose constructor
// registers an Observable-gauge callback on every build (auth's
// signing-keys gauge, collection's pending-submissions gauge: every
// other test in those packages calls the constructor too, each
// registering its own callback against whatever provider is current).
// Restoring the actual prior provider is the one choice that never
// strands another test's registration mid-suite, so it is the one
// every adopter now gets.
func Install(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		otel.SetMeterProvider(prev)
		_ = mp.Shutdown(context.Background())
	})
	return reader
}

// Collect drains reader into a snapshot, failing the test on error.
// A callback-backed instrument (an Observable gauge) can make Collect
// return an error while still partially populating rm; a caller that
// must tolerate that - proving a failed callback recorded nothing
// rather than a false zero - collects through reader directly and
// hands the possibly error-degraded result to ByName instead of
// calling Collect.
func Collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	return rm
}

// ByName returns the named instrument from rm - the first match
// across every scope - and whether it was found.
func ByName(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

// ScopeMetrics collects reader and returns every instrument
// registered under the meter scope named scope, keyed by name - a
// bulk fetch for suites that assert several named metrics out of one
// collection instead of re-collecting per name.
func ScopeMetrics(t *testing.T, reader *sdkmetric.ManualReader, scope string) map[string]metricdata.Metrics {
	t.Helper()
	rm := Collect(t, reader)
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != scope {
			continue
		}
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

// Int64Points collects reader and returns the named Sum[int64]
// instrument's data points, nil when the instrument was never
// registered or never recorded. Several adopted suites assert on that
// absence, so it is not treated as a failure.
func Int64Points(t *testing.T, reader *sdkmetric.ManualReader, name string) []metricdata.DataPoint[int64] {
	t.Helper()
	m, ok := ByName(Collect(t, reader), name)
	if !ok {
		return nil
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s: data type %T, want Sum[int64]", name, m.Data)
	}
	return sum.DataPoints
}

// HasAttrs reports whether set carries every key/value in want -
// containment, not equality, so a caller can match a series without
// naming every attribute it carries.
func HasAttrs(set attribute.Set, want []attribute.KeyValue) bool {
	for _, kv := range want {
		v, ok := set.Value(kv.Key)
		if !ok || v.String() != kv.Value.String() {
			return false
		}
	}
	return true
}

// Int64Sum collects reader and totals the named Sum[int64]
// instrument's points whose attributes carry every one of want (every
// point when want is empty). 0 when the instrument was never
// registered, never recorded, or no point matches.
func Int64Sum(t *testing.T, reader *sdkmetric.ManualReader, name string, want ...attribute.KeyValue) int64 {
	t.Helper()
	m, ok := ByName(Collect(t, reader), name)
	if !ok {
		return 0
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s: data type %T, want Sum[int64]", name, m.Data)
	}
	var total int64
	for _, dp := range sum.DataPoints {
		if HasAttrs(dp.Attributes, want) {
			total += dp.Value
		}
	}
	return total
}

// Float64HistogramPoint collects reader and returns the first point
// of the named Histogram[float64] instrument whose attributes carry
// every one of want (the zero value, Count 0, when none match or the
// instrument was never registered).
func Float64HistogramPoint(t *testing.T, reader *sdkmetric.ManualReader, name string, want ...attribute.KeyValue) metricdata.HistogramDataPoint[float64] {
	t.Helper()
	m, ok := ByName(Collect(t, reader), name)
	if !ok {
		return metricdata.HistogramDataPoint[float64]{}
	}
	hist, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("%s: data type %T, want Histogram[float64]", name, m.Data)
	}
	for _, dp := range hist.DataPoints {
		if HasAttrs(dp.Attributes, want) {
			return dp
		}
	}
	return metricdata.HistogramDataPoint[float64]{}
}

// Float64GaugePoint collects reader and returns the first point of the
// named Gauge[float64] instrument (a callback-backed Observable gauge)
// whose attributes carry every one of want (the zero value, Value 0,
// when none match or the instrument was never registered) - the same
// first-match/zero-value-on-absence contract Float64HistogramPoint
// already gives a recorded histogram, extended to a gauge whose points
// come from a registered callback instead of a Record call. A caller
// asserting a callback observed nothing for a given attribute set (an
// enrichment refresh step that has never completed in this process,
// for instance) reads that absence through the same zero value, with
// no separate "not observed" API.
func Float64GaugePoint(t *testing.T, reader *sdkmetric.ManualReader, name string, want ...attribute.KeyValue) metricdata.DataPoint[float64] {
	t.Helper()
	m, ok := ByName(Collect(t, reader), name)
	if !ok {
		return metricdata.DataPoint[float64]{}
	}
	gauge, ok := m.Data.(metricdata.Gauge[float64])
	if !ok {
		t.Fatalf("%s: data type %T, want Gauge[float64]", name, m.Data)
	}
	for _, dp := range gauge.DataPoints {
		if HasAttrs(dp.Attributes, want) {
			return dp
		}
	}
	return metricdata.DataPoint[float64]{}
}
