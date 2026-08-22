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
// specval's request-schema validation, and hands it to
// httpkit.NewRouter, which wires the rest: Recover -> otelhttp span ->
// RequestLogger -> mux, with /healthz and /readyz outside JWT auth and
// every API route (including POST /internal/refresh) inside it.
//
// specval sits AFTER jwtauth (it never enforces auth; jwtauth's 401
// keeps precedence) and wraps only the generated API handler, so a
// route the spec has nothing to say about - a 404 or a 405 - still
// passes through to the generated mux untouched.
//
// /internal/refresh sits behind the same blanket JWT middleware as
// every other route: the CronJob authenticates as a service token
// (minted by auth's /internal/service-token), which InternalRefresh's
// own requireService check then requires. The NetworkPolicy is the
// outer layer: it admits the CronJob pod and the known service
// callers, and the gateway never routes here (it publishes only the
// bff, which does not proxy this path).
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
	authed := jwtauth.Middleware(v, problemEW)(validate(apiRoutes))
	return httpkit.NewRouter("enrichment", authed, apiMux, logger, ready), nil
}

func problemEW(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	problem(w, r, status, code, detail)
}
