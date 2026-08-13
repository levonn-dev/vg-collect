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

// TestInstall_RecordsAndCollects drives the whole point of Install: a
// counter created after it must be readable back out through the
// returned reader.
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

// TestInstall_RestoresPriorProvider pins the sanctioned restore-previous
// semantics: cleanup must put back the exact provider that was global
// before Install ran, not a fresh blank or noop one.
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

// TestInstall_ShutsDownInstalledProvider pins the other half of the
// careful restore: the provider Install installed must be shut down on
// cleanup, not merely abandoned. A shut-down ManualReader refuses
// further Collect calls, which is the only externally observable
// signal Shutdown ran.
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

// runFatal runs f against a standalone *testing.T with no parent, so
// f's own t.Fatal cannot fail the caller's test - (*testing.T).Fail
// walks up the parent chain unconditionally, so a t.Run subtest is not
// enough to contain it. f runs in its own goroutine because Fatal's
// runtime.Goexit only unwinds the goroutine that called it.
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

// TestCollect_FatalsOnError pins Collect's fatal-on-error contract: a
// registered callback that errors must fail the test immediately,
// since every adopted suite except one (which collects through reader
// directly for this exact reason - see Collect's doc comment) treats
// a Collect error as its own bug to catch right away.
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

// TestByName_FindsAcrossScopesAndReportsMissing pins the no-scope-filter
// lookup family (auth/server and user's shared shape): the first match
// wins regardless of which scope carries it, and a missing name reports
// false rather than a zero value silently mistaken for "found".
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

// TestScopeMetrics_FiltersByScope pins the bulk-fetch family
// (collection's shape): two meters registering the SAME instrument
// name under different scopes must not merge in ScopeMetrics's result
// for either scope - the whole reason a caller reaches for scope
// filtering instead of the simpler ByName.
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

// TestInt64Points_AbsentInstrumentReturnsNil pins the nil-not-fatal
// absence contract: several adopted suites assert a counter never
// recorded by checking for a nil/empty slice, not by treating absence
// as a test failure.
func TestInt64Points_AbsentInstrumentReturnsNil(t *testing.T) {
	reader := metrictest.Install(t)
	if pts := metrictest.Int64Points(t, reader, "never.recorded"); pts != nil {
		t.Fatalf("points = %+v, want nil", pts)
	}
}

// TestInt64Points_FatalsOnWrongType pins the type-safety net: asking
// for Sum[int64] points from a histogram must fail the test loudly
// instead of silently returning nothing or panicking.
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

// TestHasAttrs pins containment semantics (enrichment's shape): a
// match requires every wanted key/value to be present, but tolerates
// extra attributes the series carries beyond what was asked for.
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

// TestInt64Sum_SumsMatchingPointsAndZeroWhenAbsent pins the
// partial-match summing family (enrichment's shape): the total folds
// every series carrying the wanted attributes, an empty want sums
// everything, and a never-recorded name is 0, not a fatal.
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

// TestFloat64HistogramPoint_FirstMatchAndZeroValueWhenAbsent pins the
// first-match extraction family (enrichment's shape): the point
// carrying the wanted attributes comes back, and both "no point
// matches" and "the instrument was never registered" come back as the
// same zero value (Count 0), not a fatal.
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
