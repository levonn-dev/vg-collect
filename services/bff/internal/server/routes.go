package server

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
	"github.com/levonn-dev/vgkeep/libs/go/specval"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
)

// maxBodyBytes bounds every /api/ request body specval reads; matches the
// largest cap any bff route enforces (telemetry relay's 1MiB, handlers_telemetry.go).
const maxBodyBytes = 1 << 20

// bffSpecPatches rewrites codegen's dangling internal $refs (see bffSpec).
// fixedWant counts every place the fixed ref must appear: handle in
// Me/UpdateMeRequest/ProfileCard (3), profile_visibility likewise (3).
var bffSpecPatches = []struct {
	preImage  []byte
	fixed     []byte
	fixedWant int
}{
	{
		preImage:  []byte(`"handle":{"$ref":"#/components/schemas/Handle"}`),
		fixed:     []byte(`"handle":{"$ref":"#/components/schemas/common_Handle"}`),
		fixedWant: 3,
	},
	{
		preImage:  []byte(`"profile_visibility":{"$ref":"#/components/schemas/Visibility"}`),
		fixed:     []byte(`"profile_visibility":{"$ref":"#/components/schemas/common_Visibility"}`),
		fixedWant: 3,
	},
}

// bffSpec loads bff's embedded OpenAPI document, patching dangling internal
// $refs on ProfileCard's own handle/profile_visibility: codegen inlines its
// body from common.yaml but never rewrites those nested refs to the
// common_-prefixed names bff's other schemas use. Each Replace is guarded:
// pre-image gone with fixedWant met is a verified no-op (fixed upstream);
// gone with the wrong count fails loudly instead of risking specval on a
// silently wrong document. Remove once api.GetSpec() loads clean upstream.
func bffSpec() (*openapi3.T, error) {
	data, err := api.GetSpecJSON()
	if err != nil {
		return nil, err
	}
	for _, p := range bffSpecPatches {
		patched := bytes.Replace(data, p.preImage, p.fixed, 1)
		switch {
		case !bytes.Equal(patched, data):
			// The known-broken shape was found and patched.
			data = patched
		case bytes.Count(data, p.fixed) == p.fixedWant:
			// Pre-image gone and every ref already reads the fixed form: codegen bug is fixed upstream, no-op.
		default:
			return nil, fmt.Errorf("bffSpec: embedded document matches neither the known dangling-ref shape nor the known-fixed shape this workaround was written against (pre-image %q); remove or update bffSpec (services/bff/internal/server/routes.go)", p.preImage)
		}
	}
	return openapi3.NewLoader().LoadFromData(data)
}

// NewRouter wires Recover(otelhttp(RequestLogger(SecurityHeaders(CheckOrigin(
// Authenticate(mux)))))); staticHandler nil means the dev server owns the frontend and / answers 404.
//
// specval sits INSIDE Authenticate, wrapping only /api/'s generated handler
// (SPA catch-all and healthz/readyz never see it); it never enforces auth, Authenticate's 401 already ran.
//
// Readiness is unconditional: the bff has no hard dependencies (denylist
// and caches fail open); the only public service must not unpublish over a transient cache blip.
func NewRouter(h *Handlers, staticHandler http.Handler, logger *slog.Logger) (http.Handler, error) {
	spec, err := bffSpec()
	if err != nil {
		return nil, err
	}
	validate := specval.Middleware(specval.Options{Spec: spec, MaxBodyBytes: maxBodyBytes})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	apiMux := http.NewServeMux()
	apiRoutes := api.HandlerWithOptions(h, api.StdHTTPServerOptions{
		BaseRouter: apiMux,
		// Binding failures answer problem+json like everything else.
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r, http.StatusBadRequest, "invalid_param", err.Error())
		},
	})
	mux.Handle("/api/", validate(apiRoutes))

	if staticHandler != nil {
		mux.Handle("/", staticHandler)
	} else {
		mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeProblem(w, r, http.StatusNotFound, "not_found",
				"static serving is disabled; the dev server owns the frontend")
		}))
	}

	handler := h.Authenticate(mux)
	handler = h.CheckOrigin(handler)
	handler = h.SecurityHeaders(handler)
	handler = httpkit.RequestLogger(logger)(handler)
	handler = otelhttp.NewHandler(httpkit.RouteLabel(handler, apiMux, mux), "bff")
	return httpkit.Recover(logger)(handler), nil
}
