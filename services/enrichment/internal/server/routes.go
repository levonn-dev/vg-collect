package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/libs/go/specval"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
)

// NewRouter builds the API handler behind jwtauth.Middleware and
// specval validation; httpkit.NewRouter then wires Recover -> otelhttp
// -> RequestLogger -> mux, with /healthz and /readyz outside JWT auth.
//
// specval sits AFTER jwtauth (auth keeps 401 precedence) and wraps
// only the generated API handler, so a 404/405 passes through untouched.
//
// /internal/refresh sits behind the same blanket JWT as every route;
// the CronJob authenticates as a service token, which requireService
// then requires. NetworkPolicy is the outer layer; the gateway never
// routes here (it publishes only the bff).
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
	return httpkit.NewRouter("enrichment", authed, apiMux, logger, ready), nil
}
