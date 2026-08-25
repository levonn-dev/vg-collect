package otel_test

// Tests for Counter and Histogram: each proves the registered instrument's name/description/
// unit land in the SDK's own fields, and that DurationBuckets is the exact shared tuple.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
)

const testScope = "github.com/levonn-dev/vgkeep/libs/go/otel_test"

// newTestMeter returns a real SDK meter draining into a fresh ManualReader. Counter and
// Histogram take an explicit metric.Meter, so tests build their own provider instead of
// swapping the global.
func newTestMeter(t *testing.T) (metric.Meter, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	return provider.Meter(testScope), reader
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collect: %v", err)
	}
	return rm
}

func metricByName(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

// stubErrMeter refuses every instrument registration, to pin the best-effort (log-and-continue)
// registration contract Counter/Histogram preserve.
type stubErrMeter struct{ noop.Meter }

func (stubErrMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errors.New("registration refused")
}

func (stubErrMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return nil, errors.New("registration refused")
}

func TestCounter_RegistersNameDescriptionUnit(t *testing.T) {
	m, reader := newTestMeter(t)
	c, err := vgotel.Counter(m, "vg.test.widgets", "Widgets processed", "{widget}")
	if err != nil {
		t.Fatalf("Counter: %v", err)
	}
	c.Add(context.Background(), 1, metric.WithAttributes(attribute.String("outcome", "ok")))

	got, ok := metricByName(collect(t, reader), "vg.test.widgets")
	if !ok {
		t.Fatal("vg.test.widgets not exported")
	}
	if got.Description != "Widgets processed" {
		t.Fatalf("description = %q, want %q", got.Description, "Widgets processed")
	}
	if got.Unit != "{widget}" {
		t.Fatalf("unit = %q, want %q", got.Unit, "{widget}")
	}
	sum, ok := got.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("data = %T, want Sum[int64]", got.Data)
	}
	if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Fatalf("data points = %+v, want one point of 1", sum.DataPoints)
	}
}

// TestCounter_ArgumentOrderIsNameDescriptionUnit pins the fixed order itself: a future edit
// swapping the call's 2nd and 3rd arguments must fail this test, not ship a swapped field.
func TestCounter_ArgumentOrderIsNameDescriptionUnit(t *testing.T) {
	m, reader := newTestMeter(t)
	c, err := vgotel.Counter(m, "vg.test.order", "description-value", "unit-value")
	if err != nil {
		t.Fatalf("Counter: %v", err)
	}
	c.Add(context.Background(), 1) // an untouched instrument exports no series to inspect
	got, ok := metricByName(collect(t, reader), "vg.test.order")
	if !ok {
		t.Fatal("vg.test.order not exported")
	}
	if got.Description != "description-value" || got.Unit != "unit-value" {
		t.Fatalf("description=%q unit=%q, want description-value/unit-value", got.Description, got.Unit)
	}
}

func TestCounter_RegistrationErrorPropagates(t *testing.T) {
	c, err := vgotel.Counter(stubErrMeter{}, "vg.test.refused", "d", "u")
	if err == nil {
		t.Fatal("want a non-nil error from a refused registration")
	}
	if c != nil {
		t.Fatalf("counter = %v, want nil on error", c)
	}
}

// logLine decodes the single JSON log line captured from a
// slog.JSONHandler-backed buffer.
func logLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("bad log line %q: %v", buf.String(), err)
	}
	return line
}

func TestCounterLogged_Success(t *testing.T) {
	m, reader := newTestMeter(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	c := vgotel.CounterLogged(m, logger, "vg.test.logged_counter", "d", "u")
	c.Add(context.Background(), 1)

	if _, ok := metricByName(collect(t, reader), "vg.test.logged_counter"); !ok {
		t.Fatal("vg.test.logged_counter not exported")
	}
	if buf.Len() != 0 {
		t.Fatalf("want no log output on a successful registration, got %q", buf.String())
	}
}

// TestCounterLogged_RegistrationFailureLogsNameAndReturnsNil pins the uniform failure shape:
// message "counter unavailable", the name under "name", and a nil instrument.
func TestCounterLogged_RegistrationFailureLogsNameAndReturnsNil(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	c := vgotel.CounterLogged(stubErrMeter{}, logger, "vg.test.refused_counter", "d", "u")
	if c != nil {
		t.Fatalf("counter = %v, want nil on a refused registration", c)
	}

	line := logLine(t, &buf)
	if line["msg"] != "counter unavailable" || line["name"] != "vg.test.refused_counter" || line["level"] != "ERROR" {
		t.Fatalf("log line = %v, want msg=counter unavailable name=vg.test.refused_counter level=ERROR", line)
	}
}

func TestHistogram_RegistersNameDescriptionUnitAndBuckets(t *testing.T) {
	m, reader := newTestMeter(t)
	h, err := vgotel.Histogram(m, "vg.test.duration", "Seconds elapsed", "s", vgotel.DurationBuckets...)
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}
	h.Record(context.Background(), 42, metric.WithAttributes(attribute.String("step", "x")))

	got, ok := metricByName(collect(t, reader), "vg.test.duration")
	if !ok {
		t.Fatal("vg.test.duration not exported")
	}
	if got.Description != "Seconds elapsed" || got.Unit != "s" {
		t.Fatalf("description=%q unit=%q, want Seconds elapsed/s", got.Description, got.Unit)
	}
	hist, ok := got.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("data = %T, want Histogram[float64]", got.Data)
	}
	if len(hist.DataPoints) != 1 {
		t.Fatalf("data points = %d, want 1", len(hist.DataPoints))
	}
	wantBounds := []float64{1, 5, 15, 60, 300, 900, 1800}
	if !slices.Equal(hist.DataPoints[0].Bounds, wantBounds) {
		t.Fatalf("bounds = %v, want %v", hist.DataPoints[0].Bounds, wantBounds)
	}
	if hist.DataPoints[0].Sum != 42 {
		t.Fatalf("sum = %v, want 42", hist.DataPoints[0].Sum)
	}
}

// TestHistogram_NoExplicitBucketsUsesSDKDefault proves buckets is genuinely optional: omitting
// it falls through to the SDK's own default view, not a zero-width or empty boundary set.
func TestHistogram_NoExplicitBucketsUsesSDKDefault(t *testing.T) {
	m, reader := newTestMeter(t)
	h, err := vgotel.Histogram(m, "vg.test.default_buckets", "d", "u")
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}
	h.Record(context.Background(), 1)
	got, ok := metricByName(collect(t, reader), "vg.test.default_buckets")
	if !ok {
		t.Fatal("vg.test.default_buckets not exported")
	}
	hist, ok := got.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("data = %T, want Histogram[float64]", got.Data)
	}
	// Asserting shape (non-empty, not our tuple), not the SDK's literal bounds: that's the
	// SDK's contract to freeze, not this package's.
	if len(hist.DataPoints) != 1 || len(hist.DataPoints[0].Bounds) == 0 {
		t.Fatalf("want default (non-empty) boundaries, got %+v", hist.DataPoints)
	}
	if slices.Equal(hist.DataPoints[0].Bounds, vgotel.DurationBuckets) {
		t.Fatal("want the SDK default boundaries, not DurationBuckets, when no buckets are passed")
	}
}

func TestHistogram_RegistrationErrorPropagates(t *testing.T) {
	h, err := vgotel.Histogram(stubErrMeter{}, "vg.test.refused", "d", "u")
	if err == nil {
		t.Fatal("want a non-nil error from a refused registration")
	}
	if h != nil {
		t.Fatalf("histogram = %v, want nil on error", h)
	}
}

func TestHistogramLogged_Success(t *testing.T) {
	m, reader := newTestMeter(t)
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := vgotel.HistogramLogged(m, logger, "vg.test.logged_histogram", "d", "u")
	h.Record(context.Background(), 1)

	if _, ok := metricByName(collect(t, reader), "vg.test.logged_histogram"); !ok {
		t.Fatal("vg.test.logged_histogram not exported")
	}
	if buf.Len() != 0 {
		t.Fatalf("want no log output on a successful registration, got %q", buf.String())
	}
}

// TestHistogramLogged_RegistrationFailureLogsNameAndReturnsNil is CounterLogged's twin: same
// shape, "histogram unavailable" in place of "counter unavailable".
func TestHistogramLogged_RegistrationFailureLogsNameAndReturnsNil(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	h := vgotel.HistogramLogged(stubErrMeter{}, logger, "vg.test.refused_histogram", "d", "u")
	if h != nil {
		t.Fatalf("histogram = %v, want nil on a refused registration", h)
	}

	line := logLine(t, &buf)
	if line["msg"] != "histogram unavailable" || line["name"] != "vg.test.refused_histogram" || line["level"] != "ERROR" {
		t.Fatalf("log line = %v, want msg=histogram unavailable name=vg.test.refused_histogram level=ERROR", line)
	}
}

func TestDurationBuckets_SharedTuple(t *testing.T) {
	want := []float64{1, 5, 15, 60, 300, 900, 1800}
	if !slices.Equal(vgotel.DurationBuckets, want) {
		t.Fatalf("DurationBuckets = %v, want %v", vgotel.DurationBuckets, want)
	}
}
