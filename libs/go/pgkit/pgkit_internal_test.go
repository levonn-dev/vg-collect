package pgkit

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// newOfflinePool builds a pool that never dials: MinConns stays zero so
// no background connections start, and Stat() serves the configured
// MaxConns plus all-zero counters.
func newOfflinePool(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	cfg, err := pgxpool.ParseConfig("postgres://u:p@127.0.0.1:1/db")
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// installManualReader swaps the global meter provider for one draining
// into the returned reader and restores the previous provider when the
// test ends.
func installManualReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	return reader
}

func collectPoolMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		if sm.Scope.Name != meterName {
			continue
		}
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

func sumInt64(t *testing.T, got map[string]metricdata.Metrics, name string) int64 {
	t.Helper()
	m, ok := got[name]
	if !ok {
		t.Fatalf("metric %q not exported", name)
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("%s: want Sum[int64], got %T", name, m.Data)
	}
	if !sum.IsMonotonic {
		t.Fatalf("%s: want monotonic sum", name)
	}
	if len(sum.DataPoints) != 1 {
		t.Fatalf("%s: want one series, got %d", name, len(sum.DataPoints))
	}
	return sum.DataPoints[0].Value
}

func gaugeInt64(t *testing.T, got map[string]metricdata.Metrics, name string) int64 {
	t.Helper()
	m, ok := got[name]
	if !ok {
		t.Fatalf("metric %q not exported", name)
	}
	g, ok := m.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("%s: want Gauge[int64], got %T", name, m.Data)
	}
	if len(g.DataPoints) != 1 {
		t.Fatalf("%s: want one series, got %d", name, len(g.DataPoints))
	}
	return g.DataPoints[0].Value
}

func TestRegisterPoolMetrics_ReportsPoolStat(t *testing.T) {
	reader := installManualReader(t)
	pool := newOfflinePool(t, 7)
	if err := registerPoolMetrics(pool); err != nil {
		t.Fatalf("registerPoolMetrics: %v", err)
	}

	got := collectPoolMetrics(t, reader)
	if v := gaugeInt64(t, got, "vg.pgkit.pool.connections"); v != 0 {
		t.Fatalf("connections: want 0, got %d", v)
	}
	if v := gaugeInt64(t, got, "vg.pgkit.pool.connections.idle"); v != 0 {
		t.Fatalf("idle: want 0, got %d", v)
	}
	if v := gaugeInt64(t, got, "vg.pgkit.pool.connections.max"); v != 7 {
		t.Fatalf("max: want 7, got %d", v)
	}
	if v := sumInt64(t, got, "vg.pgkit.pool.acquires"); v != 0 {
		t.Fatalf("acquires: want 0, got %d", v)
	}
	if v := sumInt64(t, got, "vg.pgkit.pool.empty_acquires"); v != 0 {
		t.Fatalf("empty_acquires: want 0, got %d", v)
	}

	// The wait counter's unit drives its Prometheus name
	// (vg_pgkit_pool_acquire_wait_seconds_total), so pin it.
	wait, ok := got["vg.pgkit.pool.acquire_wait"]
	if !ok {
		t.Fatal("vg.pgkit.pool.acquire_wait not exported")
	}
	if wait.Unit != "s" {
		t.Fatalf("acquire_wait unit: want s, got %q", wait.Unit)
	}
	sum, ok := wait.Data.(metricdata.Sum[float64])
	if !ok || !sum.IsMonotonic {
		t.Fatalf("acquire_wait: want monotonic Sum[float64], got %+v", wait.Data)
	}
	if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 0 {
		t.Fatalf("acquire_wait: want single zero point, got %+v", sum.DataPoints)
	}
}

func TestRegisterPoolMetrics_SecondPoolSharesInstruments(t *testing.T) {
	reader := installManualReader(t)
	if err := registerPoolMetrics(newOfflinePool(t, 4)); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	// A second Connect in the same process re-creates instruments with
	// identical identity; that must not error and must not fork a
	// second series.
	if err := registerPoolMetrics(newOfflinePool(t, 2)); err != nil {
		t.Fatalf("second registration: %v", err)
	}
	got := collectPoolMetrics(t, reader)
	if v := sumInt64(t, got, "vg.pgkit.pool.acquires"); v != 0 {
		t.Fatalf("acquires across two idle pools: want 0, got %d", v)
	}
}

func TestRegisterPoolMetrics_NoopMeterIsHarmless(t *testing.T) {
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(noop.NewMeterProvider())
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	if err := registerPoolMetrics(newOfflinePool(t, 4)); err != nil {
		t.Fatalf("noop meter must not error: %v", err)
	}
}

func TestRegisterPoolMetrics_InstrumentError(t *testing.T) {
	errBoom := errors.New("boom")
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(stubMeterProvider{err: errBoom})
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	if err := registerPoolMetrics(newOfflinePool(t, 4)); !errors.Is(err, errBoom) {
		t.Fatalf("want instrument error, got %v", err)
	}
}

// stubMeterProvider forces instrument-creation failure: the SDK never
// fails on our fixed names, but the metric API contract allows it.
type stubMeterProvider struct {
	noop.MeterProvider
	err error
}

func (p stubMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return stubMeter{err: p.err}
}

type stubMeter struct {
	noop.Meter
	err error
}

func (m stubMeter) Int64ObservableGauge(string, ...metric.Int64ObservableGaugeOption) (metric.Int64ObservableGauge, error) {
	return nil, m.err
}
