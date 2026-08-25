// Package metrictest installs an OpenTelemetry ManualReader over the global MeterProvider for
// a test and hands back typed helpers for reading what it collects. Cleanup restores the prior
// provider rather than resetting to blank; see Install for why.
package metrictest

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Install swaps the global meter provider for a fresh SDK provider draining into the returned
// ManualReader; cleanup restores the exact prior provider (not blank or noop) and shuts it down.
// Restoring matters because some constructors register an Observable-gauge callback against
// whatever provider is current, and resetting to blank would strand other tests' registrations.
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

// Collect drains reader into a snapshot, failing the test on error. A callback-backed
// instrument can make Collect return an error while partially populating rm; a caller that
// must tolerate that (proving a failed callback recorded nothing) should collect through
// reader directly.
func Collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	return rm
}

// ByName returns the named instrument from rm, the first match across every scope, and whether it was found.
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

// ScopeMetrics collects reader and returns every instrument under the named meter scope,
// keyed by name, for suites asserting several metrics from one collection.
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

// Int64Points collects reader and returns the named Sum[int64] instrument's data points, nil
// when never registered or recorded (not treated as a failure).
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

// HasAttrs reports whether set carries every key/value in want (containment, not equality).
func HasAttrs(set attribute.Set, want []attribute.KeyValue) bool {
	for _, kv := range want {
		v, ok := set.Value(kv.Key)
		if !ok || v.String() != kv.Value.String() {
			return false
		}
	}
	return true
}

// Int64Sum collects reader and totals the named Sum[int64] instrument's points matching every
// attribute in want (all points if empty); 0 when unregistered, unrecorded, or unmatched.
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

// Float64HistogramPoint collects reader and returns the first point of the named
// Histogram[float64] instrument matching every attribute in want (zero value if none match).
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

// Float64GaugePoint collects reader and returns the first point of the named Gauge[float64]
// instrument (a callback-backed Observable gauge) matching every attribute in want, the zero
// value if none match - the same absence-as-zero contract as Float64HistogramPoint, since
// there is no separate "not observed" API.
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
