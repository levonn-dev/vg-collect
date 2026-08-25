package valkeykit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestConnect_InstrumentErrorsCloseClient(t *testing.T) {
	t.Cleanup(func() {
		instrumentTracing = redisotel.InstrumentTracing
		instrumentMetrics = redisotel.InstrumentMetrics
	})

	instrumentTracing = func(redis.UniversalClient, ...redisotel.TracingOption) error {
		return errors.New("boom")
	}
	_, err := Connect(context.Background(), "redis://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "valkeykit: tracing") {
		t.Fatalf("want wrapped tracing error, got %v", err)
	}

	instrumentTracing = redisotel.InstrumentTracing
	instrumentMetrics = func(redis.UniversalClient, ...redisotel.MetricsOption) error {
		return errors.New("boom")
	}
	_, err = Connect(context.Background(), "redis://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "valkeykit: metrics") {
		t.Fatalf("want wrapped metrics error, got %v", err)
	}
}

// newOfflineClient builds a client that only ever fails to dial (port 1 refuses; retries off).
func newOfflineClient(t *testing.T) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// installManualReader swaps the global meter provider for one draining into the returned
// reader, restored on cleanup.
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

func TestRegisterPoolMetrics_ReportsPoolStats(t *testing.T) {
	reader := installManualReader(t)
	client := newOfflineClient(t)
	if err := registerPoolMetrics(client); err != nil {
		t.Fatalf("registerPoolMetrics: %v", err)
	}

	// One refused command: the pool records a miss (no free connection existed), no hit.
	_ = client.Ping(context.Background()).Err()

	got := collectPoolMetrics(t, reader)
	if v := sumInt64(t, got, "vg.valkeykit.pool.misses"); v < 1 {
		t.Fatalf("misses after failed dial: want >= 1, got %d", v)
	}
	if v := sumInt64(t, got, "vg.valkeykit.pool.hits"); v != 0 {
		t.Fatalf("hits: want 0, got %d", v)
	}
	if v := sumInt64(t, got, "vg.valkeykit.pool.timeouts"); v != 0 {
		t.Fatalf("timeouts: want 0, got %d", v)
	}
	if v := gaugeInt64(t, got, "vg.valkeykit.pool.connections"); v != 0 {
		t.Fatalf("connections: want 0, got %d", v)
	}
	if v := gaugeInt64(t, got, "vg.valkeykit.pool.connections.idle"); v != 0 {
		t.Fatalf("idle: want 0, got %d", v)
	}
}

func TestRegisterPoolMetrics_SecondClientSharesInstruments(t *testing.T) {
	reader := installManualReader(t)
	if err := registerPoolMetrics(newOfflineClient(t)); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	// A second connect re-creates instruments with identical identity; must not error or fork a series.
	if err := registerPoolMetrics(newOfflineClient(t)); err != nil {
		t.Fatalf("second registration: %v", err)
	}
	got := collectPoolMetrics(t, reader)
	if v := sumInt64(t, got, "vg.valkeykit.pool.hits"); v != 0 {
		t.Fatalf("hits across two idle clients: want 0, got %d", v)
	}
}

func TestRegisterPoolMetrics_NoopMeterIsHarmless(t *testing.T) {
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(noop.NewMeterProvider())
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	if err := registerPoolMetrics(newOfflineClient(t)); err != nil {
		t.Fatalf("noop meter must not error: %v", err)
	}
}

func TestRegisterPoolMetrics_InstrumentError(t *testing.T) {
	errBoom := errors.New("boom")
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(stubMeterProvider{err: errBoom})
	t.Cleanup(func() { otel.SetMeterProvider(prev) })
	if err := registerPoolMetrics(newOfflineClient(t)); !errors.Is(err, errBoom) {
		t.Fatalf("want instrument error, got %v", err)
	}
}

// stubMeterProvider forces instrument-creation failure for the InstrumentError test.
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

func (m stubMeter) Int64ObservableCounter(string, ...metric.Int64ObservableCounterOption) (metric.Int64ObservableCounter, error) {
	return nil, m.err
}
