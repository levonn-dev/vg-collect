// Package otel bootstraps OpenTelemetry traces, metrics, and logs for
// vg-collect services. Import aliased (vgotel) to avoid clashing with
// the upstream go.opentelemetry.io/otel package.
package otel

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

// traceContextHandler stamps trace_id/span_id onto records logged
// inside an active span, linking Loki lines to Jaeger traces.
//
// Limitation: stamping happens at Handle time, so if the logger has an
// open WithGroup group the IDs land inside that group instead of top
// level. vg-collect services therefore do not call WithGroup on the
// root logger. The OTLP log leg is unaffected: the SDK stamps trace
// context onto the record itself from ctx, independent of groups.
type traceContextHandler struct{ inner slog.Handler }

func NewTraceContextHandler(inner slog.Handler) slog.Handler {
	return &traceContextHandler{inner: inner}
}

func (h *traceContextHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *traceContextHandler) Handle(ctx context.Context, rec slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		rec.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, rec)
}

func (h *traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceContextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h *traceContextHandler) WithGroup(name string) slog.Handler {
	return &traceContextHandler{inner: h.inner.WithGroup(name)}
}

// fanout duplicates records to several handlers (stdout JSON + OTLP).
type fanout struct{ handlers []slog.Handler }

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, rec slog.Record) error {
	var firstErr error
	for _, h := range f.handlers {
		if h.Enabled(ctx, rec.Level) {
			if err := h.Handle(ctx, rec.Clone()); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithAttrs(attrs)
	}
	return fanout{handlers: next}
}

func (f fanout) WithGroup(name string) slog.Handler {
	next := make([]slog.Handler, len(f.handlers))
	for i, h := range f.handlers {
		next[i] = h.WithGroup(name)
	}
	return fanout{handlers: next}
}
