package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/libs/go/specval"
	"github.com/levonn-dev/vgkeep/services/social/internal/gen/api"
)

// NewRouter wraps the generated API handler in jwtauth then specval
// validation; jwtauth's 401 takes precedence. /healthz and /readyz sit
// outside auth; every API route sits inside it.
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
	return httpkit.NewRouter("social", authed, apiMux, logger, ready), nil
}
