// Package specval validates incoming HTTP requests against a service's
// embedded OpenAPI spec and writes house RFC 9457 problem+json responses
// on schema failures, so every service enforces its contract the same way
// instead of hand-rolling per-handler checks.
package specval

import (
	"context"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	nethttpmiddleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

// Options configures the request-validation middleware for one service.
type Options struct {
	// Spec is the service's embedded OpenAPI document, e.g. the output of
	// the generated api package's GetSpec(). Middleware disables Host
	// validation (otherwise a valid request can 404 on a Host mismatch) by
	// building the validator from a shallow copy with Servers cleared, so
	// this value itself is never mutated.
	Spec *openapi3.T

	// MaxBodyBytes caps the request body size read during validation, via
	// http.MaxBytesReader. A value <= 0 leaves the body size unbounded.
	MaxBodyBytes int64
}

// Middleware validates requests against opts.Spec and writes a house
// problem+json response (code invalid_param or invalid_body) on schema
// failures. Requests to routes absent from the spec pass through
// untouched, so the wrapped handler's own 404/405 handling still applies.
//
// Security requirements in the spec are accepted unconditionally at this
// layer (kin-openapi requires an AuthenticationFunc to be set at all, or
// every secured operation 401s regardless of credentials): authentication
// itself is jwtauth's job, running earlier in the chain, not specval's.
func Middleware(opts Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		nhOpts := &nethttpmiddleware.Options{
			DoNotValidateServers: true,
			Options: openapi3filter.Options{
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
				MultiError:         false,
				// kin-openapi's default (false) rewrites the request body
				// in place, filling every absent property with its
				// schema default before the handler ever decodes it. That
				// erases the "absent" case a caller's own default/
				// cross-field logic (e.g. a create body's pricing_mode
				// defaulting differently by branch, or an update
				// distinguishing "field omitted" from "field explicitly
				// set to its zero value") depends on being able to see -
				// silently, since the handler has no way to tell a
				// validator-injected default from one the caller actually
				// sent. specval validates; it must never rewrite what the
				// handler goes on to read.
				SkipSettingDefaults: true,
			},
			ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, errOpts nethttpmiddleware.ErrorHandlerOpts) {
				// MatchedRoute is nil exactly when the spec has nothing to
				// say about this request (unknown path, or a known path
				// with a method the spec doesn't define for it) - both
				// the 404 and 405 cases the generated mux is responsible
				// for, not specval.
				if errOpts.MatchedRoute == nil {
					next.ServeHTTP(w, r)
					return
				}
				code, detail := encode(err)
				httpkit.WriteProblemFields(w, r, http.StatusBadRequest, code, detail)
			},
		}

		// A shallow copy so DoNotValidateServers (which nils Servers on
		// whatever *openapi3.T it is given) never mutates the caller's
		// spec - opts.Spec may be shared with, e.g., a self-hosted docs
		// endpoint that still wants its Servers field intact.
		specCopy := *opts.Spec
		specCopy.Servers = nil

		// Building the validator (in particular, the spec's gorillamux
		// router) is done once here, when the middleware is installed,
		// not per-request.
		validated := nethttpmiddleware.OapiRequestValidatorWithOptions(&specCopy, nhOpts)(next)

		if opts.MaxBodyBytes <= 0 {
			return validated
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Applied before the validator ever reads the body, so an
			// over-cap request fails during the validator's own read
			// (surfacing as a RequestError the encoder maps to
			// invalid_body) rather than reaching the handler. Only when
			// there is a body to cap: r.Body is nil for a bodyless
			// request built by hand (e.g. http.NewRequest with a nil
			// io.Reader, as every direct-ServeHTTP test harness in this
			// repo does for a GET/DELETE) - a real net/http server
			// guarantees a non-nil Body instead, but MaxBytesReader
			// itself does not check: it always returns a non-nil
			// wrapper, and reading (or closing) that wrapper panics on a
			// nil underlying reader the first time anything - kin-
			// openapi's own security-requirement check reads the body
			// unconditionally - tries to read it.
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, opts.MaxBodyBytes)
			}
			validated.ServeHTTP(w, r)
		})
	}
}
