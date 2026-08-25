package otel_test

// Tests for Count and Record: a nil instrument must be a silent no-op, never a panic.

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
)

func TestCount_NilCounterIsNoop(t *testing.T) {
	vgotel.Count(context.Background(), nil, attribute.String("k", "v")) // must not panic
}

func TestCount_AddsOneWithAttributes(t *testing.T) {
	m, reader := newTestMeter(t)
	c, err := vgotel.Counter(m, "vg.test.count", "d", "u")
	if err != nil {
		t.Fatalf("Counter: %v", err)
	}
	vgotel.Count(context.Background(), c, attribute.String("outcome", "ok"))
	vgotel.Count(context.Background(), c, attribute.String("outcome", "ok"))
	vgotel.Count(context.Background(), c, attribute.String("outcome", "fail"))

	got, ok := metricByName(collect(t, reader), "vg.test.count")
	if !ok {
		t.Fatal("vg.test.count not exported")
	}
	sum, ok := got.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("data = %T, want Sum[int64]", got.Data)
	}
	want := map[string]int64{"ok": 2, "fail": 1}
	if len(sum.DataPoints) != 2 {
		t.Fatalf("data points = %+v, want 2 series", sum.DataPoints)
	}
	for _, dp := range sum.DataPoints {
		outcome, _ := dp.Attributes.Value(attribute.Key("outcome"))
		if dp.Value != want[outcome.AsString()] {
			t.Fatalf("outcome=%s value=%d, want %d", outcome.AsString(), dp.Value, want[outcome.AsString()])
		}
	}
}

// TestCount_NoAttributes covers a bare occurrence counter with no attribute breakdown.
func TestCount_NoAttributes(t *testing.T) {
	m, reader := newTestMeter(t)
	c, err := vgotel.Counter(m, "vg.test.count_bare", "d", "u")
	if err != nil {
		t.Fatalf("Counter: %v", err)
	}
	vgotel.Count(context.Background(), c)
	got, ok := metricByName(collect(t, reader), "vg.test.count_bare")
	if !ok {
		t.Fatal("vg.test.count_bare not exported")
	}
	sum := got.Data.(metricdata.Sum[int64])
	if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
		t.Fatalf("data points = %+v, want one point of 1", sum.DataPoints)
	}
}

// TestCount_MultipleAttributes covers tagging a single Add with three attributes.
func TestCount_MultipleAttributes(t *testing.T) {
	m, reader := newTestMeter(t)
	c, err := vgotel.Counter(m, "vg.test.count_multi", "d", "u")
	if err != nil {
		t.Fatalf("Counter: %v", err)
	}
	vgotel.Count(context.Background(), c,
		attribute.String("provider", "google"),
		attribute.String("flow", "login"),
		attribute.String("outcome", "success"))

	got, ok := metricByName(collect(t, reader), "vg.test.count_multi")
	if !ok {
		t.Fatal("vg.test.count_multi not exported")
	}
	sum := got.Data.(metricdata.Sum[int64])
	want := attribute.NewSet(
		attribute.String("provider", "google"),
		attribute.String("flow", "login"),
		attribute.String("outcome", "success"))
	if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 || !sum.DataPoints[0].Attributes.Equals(&want) {
		t.Fatalf("data points = %+v, want one point value=1 attrs=%s",
			sum.DataPoints, want.Encoded(attribute.DefaultEncoder()))
	}
}

func TestRecord_NilHistogramIsNoop(t *testing.T) {
	vgotel.Record(context.Background(), nil, 42) // must not panic
}

func TestRecord_RecordsValueWithAttributes(t *testing.T) {
	m, reader := newTestMeter(t)
	h, err := vgotel.Histogram(m, "vg.test.record", "d", "s")
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}
	vgotel.Record(context.Background(), h, 12.5, attribute.String("step", "load"))

	got, ok := metricByName(collect(t, reader), "vg.test.record")
	if !ok {
		t.Fatal("vg.test.record not exported")
	}
	hist := got.Data.(metricdata.Histogram[float64])
	if len(hist.DataPoints) != 1 || hist.DataPoints[0].Sum != 12.5 {
		t.Fatalf("data points = %+v, want one point summing 12.5", hist.DataPoints)
	}
}

// TestRecord_NoAttributes covers recording with no attributes.
func TestRecord_NoAttributes(t *testing.T) {
	m, reader := newTestMeter(t)
	h, err := vgotel.Histogram(m, "vg.test.record_bare", "d", "s")
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}
	vgotel.Record(context.Background(), h, 7)

	got, ok := metricByName(collect(t, reader), "vg.test.record_bare")
	if !ok {
		t.Fatal("vg.test.record_bare not exported")
	}
	hist := got.Data.(metricdata.Histogram[float64])
	if len(hist.DataPoints) != 1 || hist.DataPoints[0].Sum != 7 {
		t.Fatalf("data points = %+v, want one point summing 7", hist.DataPoints)
	}
}
