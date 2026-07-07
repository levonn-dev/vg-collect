package httpkit

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// routeLookup is the slice of http.ServeMux this middleware needs:
// pattern resolution without dispatch.
type routeLookup interface {
	Handler(r *http.Request) (http.Handler, string)
}

// RouteLabel stamps the matched mux pattern onto the otelhttp labeler
// and the active span as http.route, giving metrics and traces a
// bounded route dimension instead of raw URLs. Lookups are consulted
// in order and the first non-empty pattern wins, so callers pass the
// most specific mux (the generated API router) before any outer mux
// whose catch-all would shadow it.
func RouteLabel(next http.Handler, lookups ...routeLookup) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, m := range lookups {
			if _, pattern := m.Handler(r); pattern != "" {
				attr := semconv.HTTPRouteKey.String(pattern)
				labeler, _ := otelhttp.LabelerFromContext(r.Context())
				labeler.Add(attr)
				trace.SpanFromContext(r.Context()).SetAttributes(attr)
				break
			}
		}
		next.ServeHTTP(w, r)
	})
}
