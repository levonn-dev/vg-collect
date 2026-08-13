package server

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
)

// NewRouter builds the API handler behind jwtauth.Middleware and hands
// it to httpkit.NewRouter, which wires the rest: Recover -> otelhttp
// span -> RequestLogger -> mux, with /healthz and /readyz outside JWT
// auth and every API route (including POST /internal/refresh) inside
// it.
//
// /internal/refresh sits behind the same blanket JWT middleware as
// every other route: the CronJob authenticates as a service token
// (minted by auth's /internal/service-token), which InternalRefresh's
// own requireService check then requires. The NetworkPolicy is the
// outer layer: it admits the CronJob pod and the known service
// callers, and the gateway never routes here (it publishes only the
// bff, which does not proxy this path).
func NewRouter(h *Handlers, v *jwtauth.Validator, logger *slog.Logger, ready func(context.Context) error) http.Handler {
	apiMux := http.NewServeMux()
	apiRoutes := api.HandlerWithOptions(h, api.StdHTTPServerOptions{
		BaseRouter: apiMux,
		// Without this, the generated param-binding 400s are text/plain;
		// problem+json is the repo-wide error contract.
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problem(w, r, http.StatusBadRequest, "invalid_param", err.Error())
		},
	})
	authed := jwtauth.Middleware(v, problemEW)(apiRoutes)
	return httpkit.NewRouter("enrichment", authed, apiMux, logger, ready)
}

func problemEW(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	problem(w, r, status, code, detail)
}
