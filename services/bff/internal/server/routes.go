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

// maxBodyBytes bounds every /api/ request body specval reads during
// validation - the largest cap any bff route already enforces on its
// own (the browser telemetry relay's 1MiB; see handlers_telemetry.go).
const maxBodyBytes = 1 << 20

// bffSpecPatches lists the dangling internal $refs codegen's
// spec-embedding leaves on ProfileCard's own properties (see
// bffSpec's comment), each with the ref rewritten to the form its
// working siblings already carry, plus how many properties resolve to
// the target in this document once every one reads correctly - the
// count bffSpec's guard checks for. handle: Me.handle,
// UpdateMeRequest.handle, and ProfileCard.handle (3).
// profile_visibility: Me.profile_visibility,
// UpdateMeRequest.profile_visibility, and
// ProfileCard.profile_visibility (3).
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

// bffSpec loads bff's embedded OpenAPI document, patching the dangling
// internal $refs the codegen's spec-embedding leaves behind. bff.yaml
// pulls in ProfileCard wholesale from common.yaml via a bare top-level
// alias (ProfileCard: {$ref: './common.yaml#/...'}); the embedded
// document inlines that schema body verbatim, but does not rewrite
// ITS OWN nested refs (handle, profile_visibility) into the
// common_-prefixed names every other cross-file schema in this
// document already carries - so those properties are left pointing at
// bare "#/components/schemas/..." fragments that do not exist in
// bff's own namespace (only the common_-prefixed forms do). The same
// properties on Me and UpdateMeRequest, authored as direct external
// refs in bff.yaml itself rather than pulled in through another
// schema, rewrite correctly and need no patching. bff's embedded
// document carries no OTHER external ref once bundled (every $ref in
// it is already an internal fragment), so a plain in-memory load, no
// custom URI resolver, is enough once these refs match their working
// siblings.
//
// Each Replace is guarded rather than blind: if a future codegen
// upgrade fixes the underlying bundling bug, the pre-image stops
// appearing and every affected ref already reads the corrected form
// on its own - that is the one other shape this function accepts
// (logged nowhere; there is nothing left to patch, so the call
// silently becomes a no-op load). Anything else - a pre-image gone
// but the corrected form NOT at its expected count, meaning the
// document changed in some other way this workaround was never
// checked against - fails loudly instead of risking specval
// validating against a silently wrong document. Remove bffSpec (and
// this guard) once api.GetSpec() loads clean on its own: replace
// NewRouter's call to bffSpec with a plain api.GetSpec() call and
// delete bffSpecPatches above.
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
			// The pre-image is already gone AND every affected ref
			// already reads the corrected form: the codegen bug is
			// fixed upstream, and this patch is now a verified no-op.
		default:
			return nil, fmt.Errorf("bffSpec: embedded document matches neither the known dangling-ref shape nor the known-fixed shape this workaround was written against (pre-image %q); remove or update bffSpec (services/bff/internal/server/routes.go)", p.preImage)
		}
	}
	return openapi3.NewLoader().LoadFromData(data)
}

// NewRouter wires Recover(otelhttp(RequestLogger(SecurityHeaders(
// CheckOrigin(Authenticate(mux)))))). staticHandler serves the SPA at
// catch-all when static serving is on; nil means the dev server owns
// the frontend and / answers 404.
//
// specval sits INSIDE Authenticate's coverage, wrapping only the
// generated API handler mounted at /api/: the SPA catch-all and
// healthz/readyz never see it, and a route the spec has nothing to
// say about (a 404 or a 405) still passes through to the generated
// mux untouched. It never enforces auth itself - Authenticate's 401
// already ran by the time a request reaches it.
//
// Readiness is unconditional: the bff has no hard dependencies by
// design (denylist and caches fail open, downstream calls degrade
// per-request), and the only public service must not unpublish itself
// because its cache is having a moment.
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
	handler = SecurityHeaders(handler)
	handler = httpkit.RequestLogger(logger)(handler)
	handler = otelhttp.NewHandler(httpkit.RouteLabel(handler, apiMux, mux), "bff")
	return httpkit.Recover(logger)(handler), nil
}
