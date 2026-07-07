package otel_test

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// stubLogExporter records everything the SDK exports.
type stubLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (s *stubLogExporter) Export(_ context.Context, recs []sdklog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, recs...)
	return nil
}
func (s *stubLogExporter) Shutdown(context.Context) error   { return nil }
func (s *stubLogExporter) ForceFlush(context.Context) error { return nil }

func TestOTLPLogRecordsCarryTraceContext(t *testing.T) {
	exp := &stubLogExporter{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)))
	t.Cleanup(func() { _ = lp.Shutdown(context.Background()) })
	logger := slog.New(otelslog.NewHandler("test", otelslog.WithLoggerProvider(lp)))

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	logger.InfoContext(ctx, "inside span")
	span.End()

	if len(exp.records) != 1 {
		t.Fatalf("exported %d records, want 1", len(exp.records))
	}
	rec := exp.records[0]
	if rec.TraceID() != span.SpanContext().TraceID() {
		t.Fatalf("record trace id %s != span trace id %s", rec.TraceID(), span.SpanContext().TraceID())
	}
	if rec.SpanID() != span.SpanContext().SpanID() {
		t.Fatalf("record span id %s != span span id %s", rec.SpanID(), span.SpanContext().SpanID())
	}
}
