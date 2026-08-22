// Validator-path pins: each case drives a request through the FULL
// handler stack (real router, real jwtauth, no hand-faked wiring) and
// asserts the status + problem code specval's request-validation
// middleware answers with. A case whose bound was already enforced by
// a hand check passes both before and during the double-validation
// window that follows wiring, then keeps passing once that hand check
// is retired and specval alone enforces it.
//
// TestValidatorPath_CreateShelfComment_WhitespaceOnlyBody is the
// blank-after-trim case: it stays green after specval wires in AND
// after the mechanical maxLength half of CreateShelfComment's hand
// check is removed, because the contract's minLength(1) on body
// cannot reject a whitespace-only string - only the handler's own
// trim-then-check guard can. That guard is semantic and is kept.
package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestValidatorPath_CreateShelfComment_EmptyBody pins body's
// minLength(1) contract floor for the literal empty string.
func TestValidatorPath_CreateShelfComment_EmptyBody(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodPost, srv.URL+"/shelves/"+uuid.NewString()+"/comments",
		a.token(t, uuid.NewString()), map[string]string{"body": ""})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_CreateShelfComment_WhitespaceOnlyBody pins the
// blank-after-trim guard described in the file comment.
func TestValidatorPath_CreateShelfComment_WhitespaceOnlyBody(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodPost, srv.URL+"/shelves/"+uuid.NewString()+"/comments",
		a.token(t, uuid.NewString()), map[string]string{"body": "   "})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_CreateShelfComment_OversizeBody pins body's
// maxLength(2000) contract cap.
func TestValidatorPath_CreateShelfComment_OversizeBody(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodPost, srv.URL+"/shelves/"+uuid.NewString()+"/comments",
		a.token(t, uuid.NewString()), map[string]string{"body": strings.Repeat("x", 2001)})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_GetFeed_BadTabEnum pins the shared tab
// parameter's enum contract (common.yaml's tab: [following, you]).
func TestValidatorPath_GetFeed_BadTabEnum(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodGet, srv.URL+"/feed?tab=nope", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
}

// TestValidatorPath_ListShelfComments_LimitOverMax pins limit's
// maximum(50) contract cap: specval's request-validation middleware
// rejects an out-of-range limit before the handler ever runs.
func TestValidatorPath_ListShelfComments_LimitOverMax(t *testing.T) {
	srv, a := newUnitServer(t, &stubStore{}, &stubCollection{}, &stubUsers{})
	resp := do(t, http.MethodGet, srv.URL+"/shelves/"+uuid.NewString()+"/comments?limit=51",
		a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
}
