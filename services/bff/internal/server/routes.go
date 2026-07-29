package server

import (
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
)

// NewRouter wires Recover(otelhttp(RequestLogger(SecurityHeaders(
// CheckOrigin(Authenticate(mux)))))). staticHandler serves the SPA at
// catch-all when static serving is on; nil means the dev server owns
// the frontend and / answers 404.
//
// Readiness is unconditional: the bff has no hard dependencies by
// design (denylist and caches fail open, downstream calls degrade
// per-request), and the only public service must not unpublish itself
// because its cache is having a moment.
func NewRouter(h *Handlers, staticHandler http.Handler, logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	apiMux := http.NewServeMux()
	apiRoutes := api.HandlerWithOptions(h, api.StdHTTPServerOptions{
		BaseRouter: apiMux,
		// Binding failures answer problem+json like everything else.
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r, http.StatusBadRequest, "invalid_param", err.Error())
		},
	})
	mux.Handle("/api/", apiRoutes)

	if staticHandler != nil {
		mux.Handle("/", staticHandler)
	} else {
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeProblem(w, r, http.StatusNotFound, "not_found",
				"static serving is disabled; the dev server owns the frontend")
		}))
	}

	handler := h.Authenticate(mux)
	handler = h.CheckOrigin(handler)
	handler = SecurityHeaders(handler)
	handler = httpkit.RequestLogger(logger)(handler)
	handler = otelhttp.NewHandler(httpkit.RouteLabel(handler, apiMux, mux), "bff")
	return httpkit.Recover(logger)(handler)
}
