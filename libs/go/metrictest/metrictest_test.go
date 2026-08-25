package metrictest_test

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/levonn-dev/vgkeep/libs/go/metrictest"
)

// TestInstall_RecordsAndCollects pins that a counter created after Install is readable through the returned reader.
func TestInstall_RecordsAndCollects(t *testing.T) {
	reader := metrictest.Install(t)
	counter, err := otel.Meter("metrictest-test").Int64Counter("test.install.counter")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(context.Background(), 3)

	pts := metrictest.Int64Points(t, reader, "test.install.counter")
	if len(pts) != 1 || pts[0].Value != 3 {
		t.Fatalf("points = %+v, want a single point of 3", pts)
	}
}

// TestInstall_RestoresPriorProvider pins that cleanup restores the exact prior global provider, not a fresh blank or noop one.
func TestInstall_RestoresPriorProvider(t *testing.T) {
	sentinel := sdkmetric.NewMeterProvider()
	outerPrev := otel.GetMeterProvider()
	otel.SetMeterProvider(sentinel)
	t.Cleanup(func() { otel.SetMeterProvider(outerPrev) })

	t.Run("inner", func(st *testing.T) {
		metrictest.Install(st)
		if got := otel.GetMeterProvider(); got == sentinel {
			st.Fatal("Install left the sentinel provider in place instead of swapping it")
		}
	})
	if got := otel.GetMeterProvider(); got != sentinel {
		t.Fatalf("provider after cleanup = %v, want the sentinel restored", got)
	}
}

// TestInstall_ShutsDownInstalledProvider pins that cleanup shuts down the installed provider,
// observable only via a shut-down ManualReader refusing further Collect calls.
func TestInstall_ShutsDownInstalledProvider(t *testing.T) {
	var reader *sdkmetric.ManualReader
	t.Run("inner", func(st *testing.T) {
		reader = metrictest.Install(st)
	})

	var rm metricdata.ResourceMetrics
	err := reader.Collect(context.Background(), &rm)
	if !errors.Is(err, sdkmetric.ErrReaderShutdown) {
		t.Fatalf("Collect after cleanup = %v, want ErrReaderShutdown", err)
	}
}

// runFatal runs f against a standalone *testing.T in its own goroutine.
// A t.Run subtest would still propagate Fail to the caller; this doesn't.
func runFatal(f func(t *testing.T)) bool {
	sub := &testing.T{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(sub)
	}()
	<-done
	return sub.Failed()
}

// TestCollect_FatalsOnError pins that a registered callback erroring fails the test immediately.
func TestCollect_FatalsOnError(t *testing.T) {
	reader := metrictest.Install(t)
	meter := otel.Meter("metrictest-test")
	gauge, err := meter.Int64ObservableGauge("test.collect.erroring_gauge")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := meter.RegisterCallback(func(context.Context, metric.Observer) error {
		return errors.New("boom")
	}, gauge); err != nil {
		t.Fatal(err)
	}

	if !runFatal(func(st *testing.T) { metrictest.Collect(st, reader) }) {
		t.Fatal("want Collect to fail the test when a registered callback errors")
	}
}

// TestByName_FindsAcrossScopesAndReportsMissing pins that the first match wins regardless of
// scope, and a missing name reports false, not a zero value.
func TestByName_FindsAcrossScopesAndReportsMissing(t *testing.T) {
	rm := metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{
		{Scope: instrumentation.Scope{Name: "scope.a"}, Metrics: []metricdata.Metrics{{Name: "a.counter"}}},
		{Scope: instrumentation.Scope{Name: "scope.b"}, Metrics: []metricdata.Metrics{{Name: "b.counter"}}},
	}}
	m, ok := metrictest.ByName(rm, "b.counter")
	if !ok || m.Name != "b.counter" {
		t.Fatalf("ByName(b.counter) = %+v, %v, want the scope.b metric found", m, ok)
	}
	if _, ok := metrictest.ByName(rm, "missing"); ok {
		t.Fatal("ByName reported found for a name never present in rm")
	}
}

// TestScopeMetrics_FiltersByScope pins that two meters sharing an instrument name under
// different scopes never merge in either scope's result.
func TestScopeMetrics_FiltersByScope(t *testing.T) {
	reader := metrictest.Install(t)
	ctx := context.Background()
	c1, err := otel.Meter("scope.one").Int64Counter("shared.name")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := otel.Meter("scope.two").Int64Counter("shared.name")
	if err != nil {
		t.Fatal(err)
	}
	c1.Add(ctx, 1)
	c2.Add(ctx, 2)

	got := metrictest.ScopeMetrics(t, reader, "scope.one")
	if len(got) != 1 {
		t.Fatalf("ScopeMetrics(scope.one) returned %d metrics, want 1 (scope.two must not leak in)", len(got))
	}
	m, ok := got["shared.name"]
	if !ok {
		t.Fatal("scope.one's own metric is missing from its result")
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Fatalf("scope.one data = %+v, want a single point of 1 (not scope.two's 2)", m.Data)
	}
}

// TestInt64Points_AbsentInstrumentReturnsNil pins that an unrecorded counter returns nil, not a fatal.
func TestInt64Points_AbsentInstrumentReturnsNil(t *testing.T) {
	reader := metrictest.Install(t)
	if pts := metrictest.Int64Points(t, reader, "never.recorded"); pts != nil {
		t.Fatalf("points = %+v, want nil", pts)
	}
}

// TestInt64Points_FatalsOnWrongType pins that requesting Sum[int64] from a histogram fails the test, not panics.
func TestInt64Points_FatalsOnWrongType(t *testing.T) {
	reader := metrictest.Install(t)
	hist, err := otel.Meter("metrictest-test").Float64Histogram("test.wrongtype.hist")
	if err != nil {
		t.Fatal(err)
	}
	hist.Record(context.Background(), 1.5)

	if !runFatal(func(st *testing.T) { metrictest.Int64Points(st, reader, "test.wrongtype.hist") }) {
		t.Fatal("want the fatal path to fire: test.wrongtype.hist is a histogram, not Sum[int64]")
	}
}

// TestHasAttrs pins containment semantics: every wanted key/value must match, extra attributes are tolerated.
func TestHasAttrs(t *testing.T) {
	set := attribute.NewSet(attribute.String("a", "1"), attribute.String("b", "2"))
	cases := []struct {
		name  string
		want  []attribute.KeyValue
		match bool
	}{
		{"empty want matches anything", nil, true},
		{"subset matches", []attribute.KeyValue{attribute.String("a", "1")}, true},
		{"full match", []attribute.KeyValue{attribute.String("a", "1"), attribute.String("b", "2")}, true},
		{"wrong value", []attribute.KeyValue{attribute.String("a", "x")}, false},
		{"missing key", []attribute.KeyValue{attribute.String("c", "1")}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := metrictest.HasAttrs(set, tc.want); got != tc.match {
				t.Fatalf("HasAttrs = %v, want %v", got, tc.match)
			}
		})
	}
}

// TestInt64Sum_SumsMatchingPointsAndZeroWhenAbsent pins that the total folds matching series,
// an empty want sums everything, and an unrecorded name is 0, not a fatal.
func TestInt64Sum_SumsMatchingPointsAndZeroWhenAbsent(t *testing.T) {
	reader := metrictest.Install(t)
	ctx := context.Background()
	counter, err := otel.Meter("metrictest-test").Int64Counter("test.sum.outcomes")
	if err != nil {
		t.Fatal(err)
	}
	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "ok")))
	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "ok")))
	counter.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", "failed")))

	if got := metrictest.Int64Sum(t, reader, "test.sum.outcomes", attribute.String("outcome", "ok")); got != 2 {
		t.Fatalf("ok sum = %d, want 2", got)
	}
	if got := metrictest.Int64Sum(t, reader, "test.sum.outcomes"); got != 3 {
		t.Fatalf("unfiltered sum = %d, want 3", got)
	}
	if got := metrictest.Int64Sum(t, reader, "never.recorded"); got != 0 {
		t.Fatalf("absent instrument sum = %d, want 0", got)
	}
}

// TestFloat64HistogramPoint_FirstMatchAndZeroValueWhenAbsent pins that the matching point comes
// back, and both "no match" and "never registered" return the same zero value, not a fatal.
func TestFloat64HistogramPoint_FirstMatchAndZeroValueWhenAbsent(t *testing.T) {
	reader := metrictest.Install(t)
	ctx := context.Background()
	hist, err := otel.Meter("metrictest-test").Float64Histogram("test.histogram.duration")
	if err != nil {
		t.Fatal(err)
	}
	hist.Record(ctx, 1.5, metric.WithAttributes(attribute.String("step", "a")))
	hist.Record(ctx, 2.5, metric.WithAttributes(attribute.String("step", "b")))

	dp := metrictest.Float64HistogramPoint(t, reader, "test.histogram.duration", attribute.String("step", "b"))
	if dp.Count != 1 || dp.Sum != 2.5 {
		t.Fatalf("point = %+v, want count 1 sum 2.5", dp)
	}
	if dp := metrictest.Float64HistogramPoint(t, reader, "test.histogram.duration", attribute.String("step", "absent")); dp.Count != 0 {
		t.Fatalf("no-match point = %+v, want the zero value", dp)
	}
	if dp := metrictest.Float64HistogramPoint(t, reader, "never.recorded"); dp.Count != 0 {
		t.Fatalf("absent instrument point = %+v, want the zero value", dp)
	}
}

// TestFloat64GaugePoint_FirstMatchAndZeroValueWhenAbsent is Float64HistogramPoint's
// callback-backed-gauge sibling: same first-match/zero-value-on-absence contract.
func TestFloat64GaugePoint_FirstMatchAndZeroValueWhenAbsent(t *testing.T) {
	reader := metrictest.Install(t)
	meter := otel.Meter("metrictest-test")
	gauge, err := meter.Float64ObservableGauge("test.gauge.last_completed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		o.ObserveFloat64(gauge, 42, metric.WithAttributes(attribute.String("step", "b")))
		return nil
	}, gauge); err != nil {
		t.Fatal(err)
	}

	dp := metrictest.Float64GaugePoint(t, reader, "test.gauge.last_completed", attribute.String("step", "b"))
	if dp.Value != 42 {
		t.Fatalf("point = %+v, want value 42", dp)
	}
	if dp := metrictest.Float64GaugePoint(t, reader, "test.gauge.last_completed", attribute.String("step", "absent")); dp.Value != 0 {
		t.Fatalf("no-match point = %+v, want the zero value", dp)
	}
	if dp := metrictest.Float64GaugePoint(t, reader, "never.recorded"); dp.Value != 0 {
		t.Fatalf("absent instrument point = %+v, want the zero value", dp)
	}
}
