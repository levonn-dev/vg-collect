package otel_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	vgotel "github.com/levonn-dev/vgkeep/libs/go/otel"
)

func TestTraceHandler_InjectsIDsInsideSpan(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(vgotel.NewTraceContextHandler(slog.NewJSONHandler(&buf, nil)))
	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	logger.InfoContext(ctx, "hello")
	span.End()
	line := buf.String()
	if !strings.Contains(line, `"trace_id":"`+span.SpanContext().TraceID().String()+`"`) {
		t.Fatalf("missing trace_id: %s", line)
	}
	if !strings.Contains(line, `"span_id":"`) {
		t.Fatalf("missing span_id: %s", line)
	}
}

func TestTraceHandler_NoSpanNoIDs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(vgotel.NewTraceContextHandler(slog.NewJSONHandler(&buf, nil)))
	logger.Info("hello")
	if strings.Contains(buf.String(), "trace_id") {
		t.Fatalf("unexpected trace_id: %s", buf.String())
	}
}

// TestTraceHandler_WithAttrs verifies that attributes added via WithAttrs
// appear in subsequent log lines.
func TestTraceHandler_WithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := vgotel.NewTraceContextHandler(slog.NewJSONHandler(&buf, nil))
	derived := h.WithAttrs([]slog.Attr{slog.String("env", "test")})
	slog.New(derived).Info("msg")
	if !strings.Contains(buf.String(), `"env":"test"`) {
		t.Fatalf("WithAttrs: attr missing in output: %s", buf.String())
	}
}

// TestTraceHandler_WithGroup verifies that WithGroup nests subsequent
// attributes under the named group key.
func TestTraceHandler_WithGroup(t *testing.T) {
	var buf bytes.Buffer
	h := vgotel.NewTraceContextHandler(slog.NewJSONHandler(&buf, nil))
	grouped := h.WithGroup("ns")
	// Attrs logged after WithGroup must be nested under "ns".
	slog.New(grouped).With("k", "v").Info("msg")
	if !strings.Contains(buf.String(), `"ns":{`) {
		t.Fatalf("WithGroup: group key missing in output: %s", buf.String())
	}
}

// TestTraceHandler_Enabled delegates to the inner handler's level filter.
func TestTraceHandler_Enabled(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := vgotel.NewTraceContextHandler(inner)
	if h.Enabled(context.Background(), slog.LevelDebug) {
		t.Fatal("Debug must not be enabled when inner handler is LevelWarn")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Fatal("Error must be enabled when inner handler is LevelWarn")
	}
}

// TestTraceHandler_WithAttrs_TracePropagates ensures trace IDs still appear
// after WithAttrs produces a derived handler.
func TestTraceHandler_WithAttrs_TracePropagates(t *testing.T) {
	var buf bytes.Buffer
	h := vgotel.NewTraceContextHandler(slog.NewJSONHandler(&buf, nil))
	derived := h.WithAttrs([]slog.Attr{slog.String("svc", "test")})
	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")
	defer span.End()
	slog.New(derived).InfoContext(ctx, "traced")
	out := buf.String()
	if !strings.Contains(out, `"trace_id":"`) {
		t.Fatalf("WithAttrs derived handler lost trace injection: %s", out)
	}
	if !strings.Contains(out, `"svc":"test"`) {
		t.Fatalf("WithAttrs derived handler lost attr: %s", out)
	}
}

// TestTraceHandler_WithGroup_TracePropagates ensures trace IDs still appear
// after WithGroup produces a derived handler.
func TestTraceHandler_WithGroup_TracePropagates(t *testing.T) {
	var buf bytes.Buffer
	h := vgotel.NewTraceContextHandler(slog.NewJSONHandler(&buf, nil))
	grouped := h.WithGroup("ns")
	tp := sdktrace.NewTracerProvider()
	ctx, span := tp.Tracer("t").Start(context.Background(), "op")
	defer span.End()
	slog.New(grouped).With("k", "v").InfoContext(ctx, "traced")
	out := buf.String()
	if !strings.Contains(out, `"trace_id":"`) {
		t.Fatalf("WithGroup derived handler lost trace injection: %s", out)
	}
}

// Pins the documented limitation: at Handle time an open WithGroup
// group swallows the stamped IDs, so they land nested rather than top
// level. The stdout leg lives with this (services never group the root
// logger); the OTLP leg is unaffected because the SDK stamps trace
// context onto the record itself from ctx.
func TestTraceHandler_WithGroup_IDsLandInsideGroup(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(vgotel.NewTraceContextHandler(slog.NewJSONHandler(&buf, nil))).WithGroup("ns")

	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("test").Start(context.Background(), "op")
	logger.InfoContext(ctx, "grouped")
	span.End()

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("output not JSON: %v", err)
	}
	if _, top := line["trace_id"]; top {
		t.Fatal("trace_id is top-level; the WithGroup limitation no longer holds - update the handler docs and this test")
	}
	ns, ok := line["ns"].(map[string]any)
	if !ok {
		t.Fatalf("no ns group object in %s", buf.String())
	}
	if _, nested := ns["trace_id"]; !nested {
		t.Fatalf("trace_id neither top-level nor in group: %s", buf.String())
	}
}
