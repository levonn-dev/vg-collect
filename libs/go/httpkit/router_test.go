package httpkit_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

func alwaysReady(context.Context) error { return nil }

func TestNewRouter_HealthzIsOpenAndOK(t *testing.T) {
	router := httpkit.NewRouter("widget-service", http.NewServeMux(), http.NewServeMux(), discardLogger(), alwaysReady)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("body = %s, want status ok", rec.Body.String())
	}
}

func TestNewRouter_ReadyzOKWhenReadyPasses(t *testing.T) {
	router := httpkit.NewRouter("widget-service", http.NewServeMux(), http.NewServeMux(), discardLogger(), alwaysReady)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ready"`) {
		t.Fatalf("body = %s, want status ready", rec.Body.String())
	}
}

func TestNewRouter_ReadyzServiceUnavailableWhenReadyFails(t *testing.T) {
	notReady := func(context.Context) error { return errors.New("db down") }
	router := httpkit.NewRouter("widget-service", http.NewServeMux(), http.NewServeMux(), discardLogger(), notReady)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "not_ready") {
		t.Fatalf("body = %s, want not_ready code", rec.Body.String())
	}
}

func TestNewRouter_MountsAPIHandlerAtRoot(t *testing.T) {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /widgets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	router := httpkit.NewRouter("widget-service", apiMux, apiMux, discardLogger(), alwaysReady)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/widgets/42", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want the apiHandler's own response to pass through", rec.Code)
	}
}

// TestNewRouter_OtelSpanPresentWithRouteFromRawMux is the regression
// test for RouteLabel resolving the matched pattern from apiMux, the
// caller's raw BaseRouter, not from apiHandler, which every real
// caller wraps in jwtauth.Middleware (or, here, a stand-in wrapper with
// no route-lookup method of its own) before handing it to NewRouter.
// This is what fails if a future change drops or misorders that
// argument.
//
// It also pins otelhttp's own span-naming behavior for this operation
// string: otelhttp v0.69's default formatter ignores the operation
// argument entirely and derives the name from the request's matched
// pattern instead (net/http's r.Pattern, populated by the same mux
// dispatch RouteLabel observes). serviceName still has to reach
// otelhttp.NewHandler correctly, since it is the value any future
// WithSpanNameFormatter override would receive, but under the default
// formatter every service's span is named "METHOD /pattern", never
// the literal service name.
func TestNewRouter_OtelSpanPresentWithRouteFromRawMux(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /widgets/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// Opaque wrapper: a plain http.HandlerFunc has no Handler(r) method,
	// exactly like the jwtauth.Middleware-wrapped handler every real
	// caller passes as apiHandler.
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { apiMux.ServeHTTP(w, r) })

	router := httpkit.NewRouter("widget-service", wrapped, apiMux, discardLogger(), alwaysReady)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/widgets/42", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	spans := exp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.SpanKind != trace.SpanKindServer {
		t.Fatalf("span kind = %v, want Server (proves otelhttp's span middleware, not some other tracer)", span.SpanKind)
	}
	if span.Name != "GET /widgets/{id}" {
		t.Fatalf("span name = %q, want otelhttp's default method+pattern name", span.Name)
	}
	var route string
	var found bool
	for _, kv := range span.Attributes {
		if kv.Key == semconv.HTTPRouteKey {
			route, found = kv.Value.AsString(), true
		}
	}
	if !found || route != "GET /widgets/{id}" {
		t.Fatalf("http.route = %q (found=%v), want the apiMux pattern", route, found)
	}
}

func TestNewRouter_RecoverCatchesPanicFromAPIHandler(t *testing.T) {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	router := httpkit.NewRouter("widget-service", apiMux, apiMux, discardLogger(), alwaysReady)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content-type = %q, want problem+json", ct)
	}
}

func TestNewRouter_RequestLoggerLogsRequest(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	router := httpkit.NewRouter("widget-service", http.NewServeMux(), http.NewServeMux(), logger, alwaysReady)

	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	line := buf.String()
	for _, want := range []string{`"method":"GET"`, `"path":"/healthz"`, `"status":200`} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line %q missing %q", line, want)
		}
	}
}
