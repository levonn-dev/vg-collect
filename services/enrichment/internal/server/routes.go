package server

import (
	"context"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/gen/api"
)

// NewRouter wires: Recover -> otelhttp (span) -> RequestLogger -> mux,
// with /healthz, /readyz and POST /internal/refresh outside JWT auth
// and every API route inside it.
//
// /internal/refresh is JWT-exempt, not unauthenticated: the CronJob
// has no JWT source (only the auth service mints, and only for
// logins), so the handler authenticates a static internal-caller
// token (X-Internal-Token, constant-time compare against an A/B
// rotatable set) instead. The NetworkPolicy is the outer layer: it
// admits the CronJob pod and the known service callers, and the
// gateway never routes here (it publishes only the bff, which does
// not proxy this path).
func NewRouter(h *Handlers, v *jwtauth.Validator, logger *slog.Logger, ready func(context.Context) error) http.Handler {
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
	mux.HandleFunc("POST /internal/refresh", h.InternalRefresh)

	apiRoutes := api.HandlerWithOptions(h, api.StdHTTPServerOptions{
		BaseRouter: http.NewServeMux(),
		// Without this, the generated param-binding 400s are text/plain;
		// problem+json is the repo-wide error contract.
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problem(w, r, http.StatusBadRequest, "invalid_param", err.Error())
		},
	})
	mux.Handle("/", jwtauth.Middleware(v, problemEW)(apiRoutes))

	handler := httpkit.RequestLogger(logger)(mux)
	handler = otelhttp.NewHandler(handler, "enrichment")
	return httpkit.Recover(logger)(handler)
}

func problemEW(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	problem(w, r, status, code, detail)
}
