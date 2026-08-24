package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/specval"
	"github.com/levonn-dev/vgkeep/services/auth/internal/gen/api"
)

// NewRouter builds the API handler behind specval's request-schema
// validation and hands it to httpkit.NewRouter, which wires the rest:
// Recover -> otelhttp span -> RequestLogger -> mux, with /healthz and
// /readyz outside specval and every API route inside it. Unlike the
// other services there is NO Bearer middleware in this chain: these
// endpoints are where tokens come from, and the JWKS must be readable
// by every service. The service is cluster-internal; network
// reachability is the access control.
func NewRouter(h *Handlers, logger *slog.Logger, ready func(context.Context) error) (http.Handler, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, err
	}
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
	return httpkit.NewRouter("auth", validate(apiRoutes), apiMux, logger, ready), nil
}
