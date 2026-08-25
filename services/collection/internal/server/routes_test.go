package server_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
	"github.com/levonn-dev/vgkeep/libs/go/jwtauthtest"
	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/services/collection/internal/server"
)

// authEnv is an in-process token mint + JWKS: real Ed25519 signatures through
// the real jwtauth middleware, no auth service needed. nil-roles-means-"user"
// is local policy; jwtauthtest.Env.Token itself leaves roles exactly as passed.
type authEnv struct {
	v   *jwtauth.Validator
	env *jwtauthtest.Env
}

func newAuthEnv(t *testing.T) authEnv {
	t.Helper()
	env := jwtauthtest.NewEnv(t)
	return authEnv{v: env.Validator, env: env}
}

// token mints a valid access token for sub (a user uuid string).
func (a authEnv) token(t *testing.T, sub string, roles ...string) string {
	t.Helper()
	if roles == nil {
		roles = []string{"user"}
	}
	return a.env.Token(t, sub, roles...)
}

// serviceToken mints a valid access token carrying token_use=service (no
// roles) for sub, the CronJob credential requireAdminOrService admits alongside an admin bearer.
func (a authEnv) serviceToken(t *testing.T, sub string) string {
	t.Helper()
	return a.env.ServiceToken(t, sub)
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// newUnitServer mounts Handlers behind the real router + middleware.
// Collaborators may be nil while a test exercises only routing/auth.
func newUnitServer(t *testing.T, st server.Store, enrich server.Enrichment, c server.Cache) (*httptest.Server, authEnv) {
	t.Helper()
	a := newAuthEnv(t)
	h := server.New(st, enrich, c, server.Options{
		DashboardCacheTTL: 5 * time.Minute,
		Logger:            testLogger(),
	})
	router, err := server.NewRouter(h, a.v, testLogger(),
		func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, a
}

// do issues a request with an optional bearer and returns the response.
func do(t *testing.T, method, url, bearer string, body io.Reader) *http.Response {
	t.Helper()
	req := reqtest.NewJSONRequest(t, method, url, bearer, body)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// wantProblem asserts a problem+json answer with the given code.
func wantProblem(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()
	reqtest.AssertProblem(t, resp, status, code)
}

func TestUnitHealthEndpointsAreOpen(t *testing.T) {
	srv, _ := newUnitServer(t, nil, nil, nil)
	for _, path := range []string{"/healthz", "/readyz"} {
		resp := do(t, http.MethodGet, srv.URL+path, "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
}

func TestUnitReadyzFailsWhenNotReady(t *testing.T) {
	a := newAuthEnv(t)
	h := server.New(nil, nil, nil, server.Options{
		DashboardCacheTTL: 5 * time.Minute,
		Logger:            testLogger(),
	})
	router, err := server.NewRouter(h, a.v, testLogger(),
		func(context.Context) error { return errors.New("not ready") })
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	resp := do(t, http.MethodGet, srv.URL+"/readyz", "", nil)
	wantProblem(t, resp, http.StatusServiceUnavailable, "not_ready")

	resp = do(t, http.MethodGet, srv.URL+"/healthz", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz: %d", resp.StatusCode)
	}
}

func TestUnitAPIRoutesRequireJWT(t *testing.T) {
	srv, a := newUnitServer(t, nil, nil, nil)
	paths := []struct{ method, path string }{
		{http.MethodGet, "/entries"},
		{http.MethodGet, "/entries/" + uuid.NewString()},
		{http.MethodGet, "/tags"},
		{http.MethodGet, "/views"},
		{http.MethodGet, "/dashboard"},
		{http.MethodGet, "/library/summary"},
		{http.MethodDelete, "/user-data"},
	}
	for _, p := range paths {
		resp := do(t, p.method, srv.URL+p.path, "", nil)
		wantProblem(t, resp, http.StatusUnauthorized, "missing_token")
	}
	// A valid token passes the gate: whatever the handler answers, the middleware
	// must not (401/403 would mean auth swallowed it).
	for _, p := range paths {
		resp := do(t, p.method, srv.URL+p.path, a.token(t, uuid.NewString()), nil)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			t.Fatalf("%s %s: a valid token must pass the middleware, got %d", p.method, p.path, resp.StatusCode)
		}
	}
}

func TestUnitBadParamIsProblemJSON(t *testing.T) {
	srv, a := newUnitServer(t, nil, nil, nil)
	resp := do(t, http.MethodGet, srv.URL+"/entries/not-a-uuid", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
}

// TestUnitInternalResnapshotRequiresJWT pins that resnapshot rides the SAME
// blanket jwtauth guard as every other route (its own admin-or-service check
// runs after, inside the handler); a bearer-less request must 401.
func TestUnitInternalResnapshotRequiresJWT(t *testing.T) {
	srv, _ := newUnitServer(t, nil, nil, nil)
	resp := do(t, http.MethodPost, srv.URL+"/internal/resnapshot", "", nil)
	wantProblem(t, resp, http.StatusUnauthorized, "missing_token")
}

// TestUnitInternalRematchEntriesRequiresJWT pins that the entry rematch rides
// the SAME blanket jwtauth guard as every other route; a bearer-less request must 401.
func TestUnitInternalRematchEntriesRequiresJWT(t *testing.T) {
	srv, _ := newUnitServer(t, nil, nil, nil)
	resp := do(t, http.MethodPost, srv.URL+"/internal/rematch-entries", "", nil)
	wantProblem(t, resp, http.StatusUnauthorized, "missing_token")
}

func TestUnitInternalNormalizePlatformsRequiresJWT(t *testing.T) {
	srv, _ := newUnitServer(t, &stubStore{}, &stubEnrichment{}, newStubCache())
	resp := do(t, http.MethodPost, srv.URL+"/internal/normalize-platforms", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("tokenless normalize: %d, want 401", resp.StatusCode)
	}
}
