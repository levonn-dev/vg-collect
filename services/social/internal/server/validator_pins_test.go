// Each case drives a request through the full handler stack and asserts
// the status/code specval answers with. WhitespaceOnlyBody is the
// exception: minLength(1) can't catch it, so the handler's trim guard does.
package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestValidatorPath_CreateShelfComment_EmptyBody pins minLength(1)'s floor.
func TestValidatorPath_CreateShelfComment_EmptyBody(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodPost, srv.URL+"/shelves/"+uuid.NewString()+"/comments",
		a.token(t, uuid.NewString()), map[string]string{"body": ""})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_CreateShelfComment_WhitespaceOnlyBody pins the
// blank-after-trim guard (see the file comment).
func TestValidatorPath_CreateShelfComment_WhitespaceOnlyBody(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodPost, srv.URL+"/shelves/"+uuid.NewString()+"/comments",
		a.token(t, uuid.NewString()), map[string]string{"body": "   "})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_CreateShelfComment_OversizeBody pins maxLength(2000)'s cap.
func TestValidatorPath_CreateShelfComment_OversizeBody(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodPost, srv.URL+"/shelves/"+uuid.NewString()+"/comments",
		a.token(t, uuid.NewString()), map[string]string{"body": strings.Repeat("x", 2001)})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_GetFeed_BadTabEnum pins tab's enum (common.yaml:
// [following, you]).
func TestValidatorPath_GetFeed_BadTabEnum(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodGet, srv.URL+"/feed?tab=nope", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
}

// TestValidatorPath_ListShelfComments_LimitOverMax pins limit's maximum(50);
// specval rejects it before the handler runs.
func TestValidatorPath_ListShelfComments_LimitOverMax(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodGet, srv.URL+"/shelves/"+uuid.NewString()+"/comments?limit=51",
		a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
}
