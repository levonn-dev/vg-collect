package httpkit

import (
	"context"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// NewRouter assembles the standard vgkeep service router: Recover -> otelhttp -> RequestLogger
// -> mux, with GET /healthz and GET /readyz exposed outside apiHandler's own middleware (so
// they answer even if, e.g., a JWT check would reject the request), and apiHandler mounted at
// "/" for everything else; readyz backs its answer with ready.
// apiMux is the *http.ServeMux apiHandler was built on, threaded separately since a wrapped
// apiHandler has no route-lookup of its own; RouteLabel needs it to resolve the matched route.
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
