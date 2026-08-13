package httpkit

import (
	"context"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// NewRouter assembles the standard vgkeep service router: Recover ->
// otelhttp (span) -> RequestLogger -> mux, with GET /healthz and
// GET /readyz exposed outside apiHandler's own middleware chain (so
// they still answer when, e.g., a JWT check inside apiHandler would
// reject the request) and apiHandler mounted at "/" for everything
// else. readyz backs its answer with ready, checking whatever
// dependency the caller considers load-bearing.
//
// serviceName becomes otelhttp's operation label. apiMux is the
// *http.ServeMux apiHandler was built on (the BaseRouter passed to the
// generated api.HandlerWithOptions), threaded through separately
// rather than derived from apiHandler: every real caller wraps
// apiHandler in further middleware (jwtauth.Middleware today) before
// passing it here, and that wrapping has no route-lookup method of its
// own. RouteLabel needs the unwrapped mux to resolve the matched
// pattern, so the span and metrics carry the real route instead of
// nothing at all.
func NewRouter(serviceName string, apiHandler http.Handler, apiMux *http.ServeMux, logger *slog.Logger, ready func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := ready(r.Context()); err != nil {
			WriteProblemFields(w, r, http.StatusServiceUnavailable, "not_ready", "dependency not ready")
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.Handle("/", apiHandler)

	handler := RequestLogger(logger)(mux)
	handler = otelhttp.NewHandler(RouteLabel(handler, apiMux, mux), serviceName)
	return Recover(logger)(handler)
}
