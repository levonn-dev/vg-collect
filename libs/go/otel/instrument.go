package otel

import (
	"log/slog"

	"go.opentelemetry.io/otel/metric"
)

// DurationBuckets are the explicit histogram bucket boundaries (seconds) for multi-minute
// background-job duration histograms. The SDK's default boundaries top out at 10s and would
// flatten a multi-minute run into the last bucket. Read-only: Histogram passes it straight through.
var DurationBuckets = []float64{1, 5, 15, 60, 300, 900, 1800}

// Counter registers an Int64Counter on m with one fixed argument order (name, description,
// unit), fixing the fleet's registration footgun where per-service closures swapped which
// string landed in WithDescription vs WithUnit. Registration is best-effort by convention: a
// non-nil error means a nil counter, which Count treats as a no-op.
func Counter(m metric.Meter, name, description, unit string) (metric.Int64Counter, error) {
	return m.Int64Counter(name, metric.WithDescription(description), metric.WithUnit(unit))
}

// CounterLogged is Counter plus a fixed failure response: a registration error logs "counter
// unavailable" keyed by name, returning the nil instrument instead of the error. Every service
// registers through this so a fleet-wide log query has one message shape.
func CounterLogged(m metric.Meter, logger *slog.Logger, name, description, unit string) metric.Int64Counter {
	c, err := Counter(m, name, description, unit)
	if err != nil {
		logger.Error("counter unavailable", "name", name, "err", err)
	}
	return c
}

// Histogram registers a Float64Histogram on m with Counter's fixed argument order, plus
// optional explicit bucket boundaries (pass DurationBuckets for the shared multi-minute-job
// shape). Registration is best-effort, same contract as Counter.
func Histogram(m metric.Meter, name, description, unit string, buckets ...float64) (metric.Float64Histogram, error) {
	opts := []metric.Float64HistogramOption{metric.WithDescription(description), metric.WithUnit(unit)}
	if len(buckets) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(buckets...))
	}
	return m.Float64Histogram(name, opts...)
}

// HistogramLogged is CounterLogged's twin for Histogram: same log line and nil-on-failure return.
func HistogramLogged(m metric.Meter, logger *slog.Logger, name, description, unit string, buckets ...float64) metric.Float64Histogram {
	h, err := Histogram(m, name, description, unit, buckets...)
	if err != nil {
		logger.Error("histogram unavailable", "name", name, "err", err)
	}
	return h
}
