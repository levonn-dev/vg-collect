package jwtauth

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey struct{}

// FromContext retrieves the validated Claims from the request context.
// Returns false if no claims are present (i.e. Middleware was not applied).
func FromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(ctxKey{}).(Claims)
	return c, ok
}

// ErrorWriter lets callers control the error body shape (e.g. httpkit
// problem+json) without jwtauth importing other vg-collect libs.
type ErrorWriter func(w http.ResponseWriter, r *http.Request, status int, code, detail string)

// Middleware returns an HTTP middleware that validates the Bearer token in
// the Authorization header. On success the Claims are stored in the request
// context for downstream handlers to retrieve via FromContext.
func Middleware(v *Validator, ew ErrorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !ok || raw == "" {
				ew(w, r, http.StatusUnauthorized, "missing_token", "Authorization: Bearer token required")
				return
			}
			claims, err := v.Validate(r.Context(), raw)
			if err != nil {
				ew(w, r, http.StatusUnauthorized, "invalid_token", "token validation failed")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, claims)))
		})
	}
}

// RequireRole returns an HTTP middleware that enforces the named role on the
// Claims already stored in context by Middleware. Must be chained after
// Middleware.
func RequireRole(role string, ew ErrorWriter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := FromContext(r.Context())
			if !ok || !claims.HasRole(role) {
				ew(w, r, http.StatusForbidden, "forbidden", "role "+role+" required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
