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
	// Spec is the service's embedded OpenAPI document (e.g. the generated api package's
	// GetSpec()). Middleware validates from a shallow copy with Servers cleared; Spec itself
	// is never mutated.
	Spec *openapi3.T

	// MaxBodyBytes caps the request body size read during validation, via
	// http.MaxBytesReader. A value <= 0 leaves the body size unbounded.
	MaxBodyBytes int64
}

// Middleware validates requests against opts.Spec and writes a house problem+json response
// (invalid_param or invalid_body) on schema failures. Routes absent from the spec pass through,
// so the wrapped handler's own 404/405 handling still applies. Security requirements are
// accepted unconditionally here (kin-openapi requires an AuthenticationFunc or every secured
// operation 401s); authentication is jwtauth's job, earlier in the chain.
func Middleware(opts Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		nhOpts := &nethttpmiddleware.Options{
			DoNotValidateServers: true,
			Options: openapi3filter.Options{
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
				MultiError:         false,
				// kin-openapi's default (false) rewrites the request body in place, filling absent
				// properties with schema defaults before the handler decodes it - silently erasing
				// the "absent" case a caller's own default/cross-field logic may depend on.
				// specval validates; it must never rewrite what the handler reads.
				SkipSettingDefaults: true,
			},
			ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, r *http.Request, errOpts nethttpmiddleware.ErrorHandlerOpts) {
				// MatchedRoute is nil exactly when the spec has nothing to say about this request
				// (unknown path, or a known path/method the spec doesn't define) - the 404/405
				// cases the generated mux owns, not specval.
				if errOpts.MatchedRoute == nil {
					next.ServeHTTP(w, r)
					return
				}
				code, detail := encode(err)
				httpkit.WriteProblemFields(w, r, http.StatusBadRequest, code, detail)
			},
		}

		// A shallow copy, since DoNotValidateServers nils Servers on whatever *openapi3.T it's
		// given; opts.Spec may be shared with e.g. a docs endpoint that wants Servers intact.
		specCopy := *opts.Spec
		specCopy.Servers = nil

		// Building the validator (the spec's gorillamux router) happens once here, not per-request.
		validated := nethttpmiddleware.OapiRequestValidatorWithOptions(&specCopy, nhOpts)(next)

		if opts.MaxBodyBytes <= 0 {
			return validated
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Applied before the validator reads the body, so an over-cap request fails during
			// the validator's own read (invalid_body) rather than reaching the handler. Guarded
			// on r.Body != nil: MaxBytesReader always wraps regardless, and reading a nil
			// underlying reader through it panics - the case for a bodyless request built by
			// hand (test harnesses' http.NewRequest with a nil io.Reader); real net/http always
			// guarantees a non-nil Body.
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, opts.MaxBodyBytes)
			}
			validated.ServeHTTP(w, r)
		})
	}
}
