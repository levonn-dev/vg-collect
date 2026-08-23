// Validator-path pins: each case drives a request through the full
// handler stack (real router, real jwtauth, no hand-faked wiring) and
// asserts the status and problem code specval's request-validation
// middleware answers with.
package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
)

// TestValidatorPath_UpdateUser_HandleTooShort pins the Handle
// schema's minLength(2) contract cap.
func TestValidatorPath_UpdateUser_HandleTooShort(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid), map[string]string{"handle": "x"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_UpdateUser_HandleBadChars pins the Handle schema's
// pattern contract (alphanumeric plus interior underscores only).
func TestValidatorPath_UpdateUser_HandleBadChars(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid), map[string]string{"handle": "bad!chars"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_UpdateUser_BadProfileVisibilityEnum pins
// UpdateUserRequest.profile_visibility's enum contract.
func TestValidatorPath_UpdateUser_BadProfileVisibilityEnum(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid), map[string]string{"profile_visibility": "public"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_UpdateUser_AvatarUrlOversize pins the
// UpdateUserRequest.avatar_url maxLength(2048) contract cap: the
// validator rejects an oversize URL before the handler runs (the
// handler keeps only the scheme/host parse, which the string schema
// cannot express).
func TestValidatorPath_UpdateUser_AvatarUrlOversize(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid),
		map[string]string{"avatar_url": "https://x.example/" + strings.Repeat("a", 2049)})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_UpdateUser_BadCurrencyEnum pins CurrencyCode's
// pattern contract (3-letter uppercase).
func TestValidatorPath_UpdateUser_BadCurrencyEnum(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid), map[string]string{"preferred_currency": "usd"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}
