package otel

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

type Config struct {
	ServiceName string
	Version     string
}

// Setup installs slog defaults (JSON to stdout with trace IDs) and, when
// OTEL_EXPORTER_OTLP_ENDPOINT is set, OTLP trace/metric/log providers.
// With the endpoint unset (unit tests, clusters without a collector) it is a
// clean no-op beyond stdout logging.
func Setup(ctx context.Context, cfg Config) (func(context.Context) error, error) {
	stdout := NewTraceContextHandler(slog.NewJSONHandler(os.Stdout, nil))
	noop := func(context.Context) error { return nil }

	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		slog.SetDefault(slog.New(stdout))
		return noop, nil
	}

	res, err := buildResource(ctx, cfg)
	if err != nil {
		return noop, err
	}

	traceExp, err := otlptracegrpc.New(ctx)
	if err != nil {
		return noop, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traceExp), sdktrace.WithResource(res))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))

	metricExp, err := otlpmetricgrpc.New(ctx)
	if err != nil {
		return noop, err
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)),
		sdkmetric.WithResource(res))
	otel.SetMeterProvider(mp)
	if err := runtime.Start(); err != nil {
		return noop, err
	}

	logExp, err := otlploggrpc.New(ctx)
	if err != nil {
		return noop, err
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExp)),
		sdklog.WithResource(res))
	global.SetLoggerProvider(lp)

	slog.SetDefault(slog.New(fanout{handlers: []slog.Handler{
		stdout,
		otelslog.NewHandler(cfg.ServiceName, otelslog.WithLoggerProvider(lp)),
	}}))

	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx), lp.Shutdown(ctx))
	}, nil
}

// buildResource identifies this process on every exported signal.
// service.instance.id comes from HOSTNAME, which Kubernetes sets to
// the pod name; outside a pod (unit tests, bare go run) it is omitted
// rather than invented.
func buildResource(ctx context.Context, cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
		semconv.ServiceVersion(cfg.Version),
	}
	if host := os.Getenv("HOSTNAME"); host != "" {
		attrs = append(attrs, semconv.ServiceInstanceID(host))
	}
	return resource.New(ctx, resource.WithAttributes(attrs...))
}
