package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Count adds 1 to c, tagged with attrs. c is nil-safe: every
// service's New logs a registration failure once and keeps the nil
// counter afterward, so every emission site used to repeat the same
// "if c == nil { return }" guard by hand (originally generalized as
// social's own count method); Count centralizes that guard so the
// per-counter wrapper methods across every service can delegate to
// it instead of restating it.
func Count(ctx context.Context, c metric.Int64Counter, attrs ...attribute.KeyValue) {
	if c == nil {
		return
	}
	c.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// Record observes value on h, tagged with attrs. h is nil-safe, same
// contract as Count, for the histogram-owning services (collection's
// entry-rematch duration, enrichment's refresh-step duration).
func Record(ctx context.Context, h metric.Float64Histogram, value float64, attrs ...attribute.KeyValue) {
	if h == nil {
		return
	}
	h.Record(ctx, value, metric.WithAttributes(attrs...))
}
