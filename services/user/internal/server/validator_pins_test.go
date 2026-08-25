// Each case drives a request through the full handler stack and asserts
// the status/code specval answers with.
package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
)

// TestValidatorPath_UpdateUser_HandleTooShort pins Handle's minLength(2) cap.
func TestValidatorPath_UpdateUser_HandleTooShort(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid), map[string]string{"handle": "x"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_UpdateUser_HandleBadChars pins Handle's pattern
// (alphanumeric plus interior underscores only).
func TestValidatorPath_UpdateUser_HandleBadChars(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid), map[string]string{"handle": "bad!chars"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_UpdateUser_BadProfileVisibilityEnum pins profile_visibility's enum.
func TestValidatorPath_UpdateUser_BadProfileVisibilityEnum(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid), map[string]string{"profile_visibility": "public"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_UpdateUser_AvatarUrlOversize pins avatar_url's
// maxLength(2048); the handler keeps only the scheme/host parse.
func TestValidatorPath_UpdateUser_AvatarUrlOversize(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid),
		map[string]string{"avatar_url": "https://x.example/" + strings.Repeat("a", 2049)})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_UpdateUser_BadCurrencyEnum pins CurrencyCode's pattern (3-letter uppercase).
func TestValidatorPath_UpdateUser_BadCurrencyEnum(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{})
	uid := uuid.NewString()
	resp := do(t, "PATCH", srv.URL+"/users/"+uid, a.token(t, uid), map[string]string{"preferred_currency": "usd"})
	reqtest.AssertProblem(t, resp, http.StatusBadRequest, "invalid_body")
}
