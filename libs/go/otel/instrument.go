package otel

import "go.opentelemetry.io/otel/metric"

// DurationBuckets are the explicit histogram bucket boundaries, in
// seconds, shared by every multi-minute background-job duration
// histogram in the fleet (collection's entry rematch, enrichment's
// catalog refresh). The SDK's default boundaries top out at 10s and
// would flatten a multi-minute run into the last bucket. Previously
// each histogram hand-copied this tuple with only a comment tying
// the copies together; treat this as read-only, since Histogram
// passes it straight through to the SDK.
var DurationBuckets = []float64{1, 5, 15, 60, 300, 900, 1800}

// Counter registers an Int64Counter on m with one fixed argument
// order: name, description, unit. That order is the whole fix for
// the fleet's registration footgun, where per-service closures took
// these same three strings in different orders, so a registration
// line copied between services compiled cleanly while silently
// swapping which string landed in WithDescription versus WithUnit.
// Registration is best-effort by convention across every caller: a
// non-nil error means the returned counter is nil, which Count (and
// every emission site before Count existed) treats as a no-op, so
// callers log the error and keep going rather than fail startup.
func Counter(m metric.Meter, name, description, unit string) (metric.Int64Counter, error) {
	return m.Int64Counter(name, metric.WithDescription(description), metric.WithUnit(unit))
}

// Histogram registers a Float64Histogram on m with the same fixed
// argument order as Counter, plus optional explicit bucket
// boundaries (pass DurationBuckets for the shared multi-minute-job
// shape; omit buckets to keep the SDK's default boundaries).
// Registration is best-effort, same contract as Counter.
func Histogram(m metric.Meter, name, description, unit string, buckets ...float64) (metric.Float64Histogram, error) {
	opts := []metric.Float64HistogramOption{metric.WithDescription(description), metric.WithUnit(unit)}
	if len(buckets) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(buckets...))
	}
	return m.Float64Histogram(name, opts...)
}
