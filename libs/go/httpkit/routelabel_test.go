package httpkit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

func routeFromLabeler(t *testing.T, l *otelhttp.Labeler) (string, bool) {
	t.Helper()
	for _, kv := range l.Get() {
		if kv.Key == semconv.HTTPRouteKey {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func TestRouteLabel_AddsMatchedPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/entries/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	labeler := &otelhttp.Labeler{}
	req := httptest.NewRequest(http.MethodGet, "/api/entries/abc", nil)
	req = req.WithContext(otelhttp.ContextWithLabeler(req.Context(), labeler))

	httpkit.RouteLabel(mux, mux).ServeHTTP(httptest.NewRecorder(), req)

	route, ok := routeFromLabeler(t, labeler)
	if !ok {
		t.Fatal("no http.route on the labeler")
	}
	if route != "GET /api/entries/{id}" {
		t.Fatalf("http.route = %q, want the mux pattern", route)
	}
}

func TestRouteLabel_FirstLookupWins(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /api/tags", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	outer := http.NewServeMux()
	outer.Handle("/api/", inner)

	labeler := &otelhttp.Labeler{}
	req := httptest.NewRequest(http.MethodGet, "/api/tags", nil)
	req = req.WithContext(otelhttp.ContextWithLabeler(req.Context(), labeler))

	// The inner (generated-API) mux is consulted first; the outer catch-all must not shadow it.
	httpkit.RouteLabel(outer, inner, outer).ServeHTTP(httptest.NewRecorder(), req)

	route, _ := routeFromLabeler(t, labeler)
	if route != "GET /api/tags" {
		t.Fatalf("http.route = %q, want the inner mux pattern", route)
	}
}

func TestRouteLabel_NoMatchAddsNothingAndStillServes(t *testing.T) {
	mux := http.NewServeMux()
	labeler := &otelhttp.Labeler{}
	req := httptest.NewRequest(http.MethodGet, "/nowhere", nil)
	req = req.WithContext(otelhttp.ContextWithLabeler(req.Context(), labeler))
	rec := httptest.NewRecorder()

	httpkit.RouteLabel(mux, mux).ServeHTTP(rec, req)

	if _, ok := routeFromLabeler(t, labeler); ok {
		t.Fatal("http.route added for an unmatched request")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want the mux 404 to pass through", rec.Code)
	}
}
