package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/specval"
	"github.com/levonn-dev/vgkeep/services/auth/internal/gen/api"
)

// NewRouter validates requests via specval, then delegates to httpkit.NewRouter (/healthz,
// /readyz exempt). No Bearer middleware: these endpoints mint tokens and serve the JWKS; cluster-internal reachability is the access control.
func NewRouter(h *Handlers, logger *slog.Logger, ready func(context.Context) error) (http.Handler, error) {
	spec, err := api.GetSpec()
	if err != nil {
		return nil, err
	}
	apiMux := http.NewServeMux()
	apiRoutes := api.HandlerWithOptions(h, api.StdHTTPServerOptions{
		BaseRouter: apiMux,
		// Without this, generated binding 400s are text/plain; problem+json is the repo-wide contract.
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problem(w, r, http.StatusBadRequest, "invalid_param", err.Error())
		},
	})
	validate := specval.Middleware(specval.Options{Spec: spec, MaxBodyBytes: maxBodyBytes})
	return httpkit.NewRouter("auth", validate(apiRoutes), apiMux, logger, ready), nil
}
