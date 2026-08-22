package server

import (
	"context"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/specval"
	"github.com/levonn-dev/vgkeep/services/auth/internal/gen/api"
)

// NewRouter wires: Recover wraps otelhttp (span) wraps RequestLogger
// wraps mux. Unlike the other services there is NO Bearer middleware:
// these endpoints are where tokens come from, and the JWKS must be
// readable by every service. The service is cluster-internal; network
// reachability is the access control.
//
// specval wraps only the generated API handler (the same subtree-only
// rule the other services' jwtauth+specval chain follows), mounted
// directly into auth's hand-rolled chain in its place; /healthz and
// /readyz stay outside it, same as every other service.
func NewRouter(h *Handlers, logger *slog.Logger, ready func(context.Context) error) (http.Handler, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := ready(r.Context()); err != nil {
			problem(w, r, http.StatusServiceUnavailable, "not_ready", "dependency not ready")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	apiMux := http.NewServeMux()
	apiRoutes := api.HandlerWithOptions(h, api.StdHTTPServerOptions{
		BaseRouter: apiMux,
		// Without this, the generated binding 400s are text/plain;
		// problem+json is the repo-wide error contract.
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problem(w, r, http.StatusBadRequest, "invalid_param", err.Error())
		},
	})
	validate := specval.Middleware(specval.Options{Spec: spec, MaxBodyBytes: maxBodyBytes})
	mux.Handle("/", validate(apiRoutes))

	handler := httpkit.RequestLogger(logger)(mux)
	handler = otelhttp.NewHandler(httpkit.RouteLabel(handler, apiMux, mux), "auth")
	return httpkit.Recover(logger)(handler), nil
}
