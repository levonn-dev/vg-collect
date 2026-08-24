package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/libs/go/specval"
	"github.com/levonn-dev/vgkeep/services/collection/internal/gen/api"
)

// NewRouter builds the API handler behind jwtauth.Middleware and
// specval's request-schema validation, and hands it to
// httpkit.NewRouter, which wires the rest: Recover -> otelhttp span ->
// RequestLogger -> mux, with /healthz and /readyz outside JWT auth and
// every API route inside it. Readiness checks Postgres only: Valkey is
// a soft dependency (the dashboard cache fails open per-request).
//
// specval sits AFTER jwtauth (it never enforces auth; jwtauth's 401
// keeps precedence) and wraps only the generated API handler, so a
// route the spec has nothing to say about - a 404 or a 405 - still
// passes through to the generated mux untouched.
func NewRouter(h *Handlers, v *jwtauth.Validator, logger *slog.Logger, ready func(context.Context) error) (http.Handler, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, err
	}
	apiMux := http.NewServeMux()
	apiRoutes := api.HandlerWithOptions(h, api.StdHTTPServerOptions{
		BaseRouter: apiMux,
		// Without this, the generated param-binding 400s are text/plain;
		// problem+json is the repo-wide error contract.
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problem(w, r, http.StatusBadRequest, "invalid_param", err.Error())
		},
	})
	validate := specval.Middleware(specval.Options{Spec: spec, MaxBodyBytes: maxBodyBytes})
	authed := jwtauth.Middleware(v, problem)(validate(apiRoutes))
	return httpkit.NewRouter("collection", authed, apiMux, logger, ready), nil
}
