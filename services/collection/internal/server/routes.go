package server

import (
	"context"
	"log/slog"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vg-collect/libs/go/httpkit"
	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	"github.com/levonn-dev/vg-collect/services/collection/internal/gen/api"
)

// NewRouter wires: Recover -> otelhttp (span) -> RequestLogger -> mux,
// with /healthz and /readyz outside JWT auth and every API route
// inside it. Readiness checks Postgres only: Valkey is a soft
// dependency (the dashboard cache fails open per-request).
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

	apiMux := http.NewServeMux()
	apiRoutes := api.HandlerWithOptions(h, api.StdHTTPServerOptions{
		BaseRouter: apiMux,
		// Without this, the generated param-binding 400s are text/plain;
		// problem+json is the repo-wide error contract.
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			problem(w, r, http.StatusBadRequest, "invalid_param", err.Error())
		},
	})
	// The one-shot release-date resnapshot: operator-invoked, so it
	// rides the normal JWT guard rather than a CronJob secret; not in
	// the contract (enrichment's /internal/refresh precedent).
	mux.Handle("POST /internal/resnapshot", jwtauth.Middleware(v, problemEW)(http.HandlerFunc(h.InternalResnapshot)))
	// The normalize-platforms lever: operator-invoked mass
	// canonicalization of free-text custom-entry platforms. Not in the
	// contract (resnapshot precedent), but admin-gated in the handler -
	// stricter than resnapshot's JWT-only guard, since it writes across
	// every user's entries.
	mux.Handle("POST /internal/normalize-platforms", jwtauth.Middleware(v, problemEW)(http.HandlerFunc(h.InternalNormalizePlatforms)))

	mux.Handle("/", jwtauth.Middleware(v, problemEW)(apiRoutes))

	handler := httpkit.RequestLogger(logger)(mux)
	handler = otelhttp.NewHandler(httpkit.RouteLabel(handler, apiMux, mux), "collection")
	return httpkit.Recover(logger)(handler)
}

func problemEW(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	problem(w, r, status, code, detail)
}
