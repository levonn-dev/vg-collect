// Validator-path pins: each case drives a request through the FULL
// handler stack - the real router built by server.NewRouter, over a
// real httptest.Server - and asserts the status + problem code
// specval's request-validation middleware answers with.
//
// newUnitRouterServer is this file's own harness, distinct from
// newEnv: none of these three cases ever reach the store (each is
// rejected before OauthCallback's ConsumeState, RefreshToken's
// PeekSession, or InternalServiceToken's mint), so a stub Store (the
// existing &stubStore{}, which panics loudly on any unexpected call)
// proves that directly and skips newEnv's Postgres dependency - the
// same fast, Docker-free posture the "fast unit layer" section below
// in handlers_test.go already established for direct handler calls,
// applied here through the real router instead.
package server_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levonn-dev/vgkeep/services/auth/internal/server"
)

// newUnitRouterServer wraps h in the real router (no jwtauth
// wrapper - see routes.go's comment) behind an httptest.Server.
func newUnitRouterServer(t *testing.T, h *server.Handlers) *httptest.Server {
	t.Helper()
	router, err := server.NewRouter(h, slog.New(slog.DiscardHandler), func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv
}

// TestValidatorPath_OauthCallback_MissingCodeKey pins
// CallbackRequest's required(code) contract for a JSON body where the
// key itself is entirely absent.
func TestValidatorPath_OauthCallback_MissingCodeKey(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	srv := newUnitRouterServer(t, h)
	resp := post(t, srv.URL+"/oauth/callback", map[string]string{"state": "s"})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_OauthCallback_EmptyCodeOrState pins
// CallbackRequest's minLength(1) contract on code and state:
// present-but-empty values are rejected by the validator before the
// handler runs (the store stub would panic if ConsumeState were ever
// reached).
func TestValidatorPath_OauthCallback_EmptyCodeOrState(t *testing.T) {
	cases := []map[string]string{
		{"code": "", "state": "s"},
		{"code": "c", "state": ""},
		{"code": "", "state": ""},
	}
	for _, body := range cases {
		h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
		srv := newUnitRouterServer(t, h)
		resp := post(t, srv.URL+"/oauth/callback", body)
		wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
	}
}

// TestValidatorPath_RefreshToken_MissingKey pins RefreshRequest's
// required(refresh_token) contract for a body where the key is
// entirely absent.
func TestValidatorPath_RefreshToken_MissingKey(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	srv := newUnitRouterServer(t, h)
	resp := post(t, srv.URL+"/token/refresh", map[string]string{})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_RefreshToken_EmptyToken pins RefreshRequest's
// minLength(1) contract: a present-but-empty refresh_token is
// rejected by the validator before the handler runs (the store stub
// would panic if PeekSession were ever reached).
func TestValidatorPath_RefreshToken_EmptyToken(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	srv := newUnitRouterServer(t, h)
	resp := post(t, srv.URL+"/token/refresh", map[string]string{"refresh_token": ""})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_InternalServiceToken_BadServiceEnum pins the
// internal service-token body's service enum contract
// (catalog-refresh, entry-rematch).
func TestValidatorPath_InternalServiceToken_BadServiceEnum(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	srv := newUnitRouterServer(t, h)
	req := jsonReq(t, http.MethodPost, srv.URL+"/internal/service-token", map[string]string{"service": "not-a-real-service"})
	req.Header.Set("X-Internal-Token", testInternalServiceToken)
	resp := send(t, req)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}
