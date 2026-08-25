package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Count adds 1 to c, tagged with attrs. c is nil-safe: a registration failure leaves a nil
// counter, so Count centralizes the "if c == nil { return }" guard every emission site needs.
func Count(ctx context.Context, c metric.Int64Counter, attrs ...attribute.KeyValue) {
	if c == nil {
		return
	}
	c.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// Record observes value on h, tagged with attrs. h is nil-safe, same contract as Count.
func Record(ctx context.Context, h metric.Float64Histogram, value float64, attrs ...attribute.KeyValue) {
	if h == nil {
		return
	}
	h.Record(ctx, value, metric.WithAttributes(attrs...))
}
