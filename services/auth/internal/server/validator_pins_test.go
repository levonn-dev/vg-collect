// Validator-path pins: each case drives a request through the real router (over a real
// httptest.Server) and asserts specval's validation status/code. None reach the store
// (rejected before ConsumeState/PeekSession/mint); the panicking stub Store proves it, skipping newEnv's Postgres dependency.
package server_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/levonn-dev/vgkeep/services/auth/internal/server"
)

// newUnitRouterServer wraps h in the real router (no jwtauth wrapper, see routes.go) behind an httptest.Server.
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

// TestValidatorPath_OauthCallback_MissingCodeKey pins CallbackRequest's required(code) contract when the key is absent.
func TestValidatorPath_OauthCallback_MissingCodeKey(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	srv := newUnitRouterServer(t, h)
	resp := post(t, srv.URL+"/oauth/callback", map[string]string{"state": "s"})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_OauthCallback_EmptyCodeOrState pins CallbackRequest's minLength(1) on
// code/state: present-but-empty is rejected before the handler (ConsumeState would panic).
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

// TestValidatorPath_RefreshToken_MissingKey pins RefreshRequest's required(refresh_token) contract when the key is absent.
func TestValidatorPath_RefreshToken_MissingKey(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	srv := newUnitRouterServer(t, h)
	resp := post(t, srv.URL+"/token/refresh", map[string]string{})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_RefreshToken_EmptyToken pins RefreshRequest's minLength(1): a
// present-but-empty refresh_token is rejected before the handler (PeekSession would panic).
func TestValidatorPath_RefreshToken_EmptyToken(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	srv := newUnitRouterServer(t, h)
	resp := post(t, srv.URL+"/token/refresh", map[string]string{"refresh_token": ""})
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// TestValidatorPath_InternalServiceToken_BadServiceEnum pins the service enum contract (catalog-refresh, entry-rematch).
func TestValidatorPath_InternalServiceToken_BadServiceEnum(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	srv := newUnitRouterServer(t, h)
	req := jsonReq(t, http.MethodPost, srv.URL+"/internal/service-token", map[string]string{"service": "not-a-real-service"})
	req.Header.Set("X-Internal-Token", testInternalServiceToken)
	resp := send(t, req)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_body")
}

// Pins the documented ordering (auth.yaml): required-param validation runs before
// internalServiceCallerOK, so a missing X-Internal-Token gets the validator's 400, never the handler's 401.
func TestValidatorPath_InternalServiceToken_MissingHeader(t *testing.T) {
	h := newUnit(&stubStore{}, unitMinter(), &stubUserService{}, nil, &stubVerifier{}, false)
	srv := newUnitRouterServer(t, h)
	req := jsonReq(t, http.MethodPost, srv.URL+"/internal/service-token", map[string]string{"service": "catalog-refresh"})
	resp := send(t, req)
	body := wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
	if body.Detail != "X-Internal-Token is required" {
		t.Fatalf("detail = %q, want %q", body.Detail, "X-Internal-Token is required")
	}
}
