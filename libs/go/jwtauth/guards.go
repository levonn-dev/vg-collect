package jwtauth

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// RequireAdminOrService reports whether the Claims already stored in
// context by Middleware carry the admin role or identify a service
// token (token_use=service): the guard on operator levers a CronJob
// can drive as a machine credential just as well as an operator can
// drive by hand. On failure it writes a 403 via ew and returns false;
// callers must stop handling the request when this returns false.
func RequireAdminOrService(w http.ResponseWriter, r *http.Request, ew ErrorWriter) bool {
	claims, _ := FromContext(r.Context())
	if claims.HasRole("admin") || claims.IsService() {
		return true
	}
	ew(w, r, http.StatusForbidden, "forbidden", "role admin or a service token is required")
	return false
}

// CallerID resolves the authenticated caller from context: the JWT
// subject parsed as the owning user id, plus the raw bearer token for
// callers that need to forward it on an outgoing hop. Must run after
// Middleware; calling it beforehand (no Claims in context) writes 401.
//
// A subject that fails to parse as a uuid writes 500: every subject
// this library validates was minted by auth as a uuid, so a bad
// subject here is a minting bug, not a caller error, and there is
// nothing the caller could have done differently.
func CallerID(w http.ResponseWriter, r *http.Request, ew ErrorWriter) (uuid.UUID, string, bool) {
	claims, ok := FromContext(r.Context())
	if !ok {
		ew(w, r, http.StatusUnauthorized, "missing_token", "no validated token in context")
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(claims.Subject)
	if err != nil {
		ew(w, r, http.StatusInternalServerError, "internal", "token subject is not a user id")
		return uuid.Nil, "", false
	}
	return id, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), true
}
