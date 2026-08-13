package jwtauth_test

import (
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
)

// recordEW is an ErrorWriter that records its arguments instead of
// writing a real problem+json body: these guard tests only need to
// prove jwtauth called ew with the right status/code/detail, the body
// shape being each adopting service's own concern (see ErrorWriter's
// doc comment).
type recordEW struct {
	called bool
	status int
	code   string
	detail string
}

func (r *recordEW) ew(w http.ResponseWriter, _ *http.Request, status int, code, detail string) {
	r.called = true
	r.status, r.code, r.detail = status, code, detail
	w.WriteHeader(status)
}

// guardEnv mints tokens against an in-process JWKS, reusing the
// genKey/jwksJSON/mint helpers from jwtauth_test.go so guard tests
// exercise the same Middleware-then-FromContext path every adopting
// service's guard call sits behind.
type guardEnv struct {
	v    *jwtauth.Validator
	priv ed25519.PrivateKey
	kid  string
}

func newGuardEnv(t *testing.T) guardEnv {
	t.Helper()
	pub, priv := genKey(t)
	const kid = "guard-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksJSON(kid, pub))
	}))
	t.Cleanup(srv.Close)
	return guardEnv{v: jwtauth.NewValidator(srv.URL, testIssuer, testAudience), priv: priv, kid: kid}
}

// runGuarded sends a bearer-token request through the real Middleware
// (so FromContext sees Claims exactly as a live handler would), then
// invokes guard inside the next handler - the same position every
// adopting service's guard call occupies.
func runGuarded(env guardEnv, token string, rec *recordEW, guard func(w http.ResponseWriter, r *http.Request) bool) bool {
	var ok bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok = guard(w, r)
		if ok {
			w.WriteHeader(http.StatusOK)
		}
	})
	mw := jwtauth.Middleware(env.v, rec.ew)(next)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	mw.ServeHTTP(httptest.NewRecorder(), r)
	return ok
}

func TestRequireAdminOrService_AdminPasses(t *testing.T) {
	env := newGuardEnv(t)
	rec := &recordEW{}
	token := mint(t, env.kid, env.priv, func(c jwt.MapClaims) { c["roles"] = []string{"admin"} })
	ok := runGuarded(env, token, rec, func(w http.ResponseWriter, r *http.Request) bool {
		return jwtauth.RequireAdminOrService(w, r, rec.ew)
	})
	if !ok || rec.called {
		t.Fatalf("admin: ok=%v ewCalled=%v, want ok=true and ew untouched", ok, rec.called)
	}
}

func TestRequireAdminOrService_ServicePasses(t *testing.T) {
	env := newGuardEnv(t)
	rec := &recordEW{}
	token := mint(t, env.kid, env.priv, func(c jwt.MapClaims) {
		delete(c, "roles")
		c["token_use"] = "service"
	})
	ok := runGuarded(env, token, rec, func(w http.ResponseWriter, r *http.Request) bool {
		return jwtauth.RequireAdminOrService(w, r, rec.ew)
	})
	if !ok || rec.called {
		t.Fatalf("service: ok=%v ewCalled=%v, want ok=true and ew untouched", ok, rec.called)
	}
}

func TestRequireAdminOrService_UserTokenForbidden(t *testing.T) {
	env := newGuardEnv(t)
	rec := &recordEW{}
	token := mint(t, env.kid, env.priv, nil) // default roles: [user]
	ok := runGuarded(env, token, rec, func(w http.ResponseWriter, r *http.Request) bool {
		return jwtauth.RequireAdminOrService(w, r, rec.ew)
	})
	if ok {
		t.Fatal("plain user token must not pass RequireAdminOrService")
	}
	if !rec.called || rec.status != http.StatusForbidden || rec.code != "forbidden" ||
		rec.detail != "role admin or a service token is required" {
		t.Fatalf("ew = %+v, want 403 forbidden with the admin-or-service detail", rec)
	}
}

func TestCallerID_HappyPath(t *testing.T) {
	env := newGuardEnv(t)
	rec := &recordEW{}
	want := uuid.New()
	token := mint(t, env.kid, env.priv, func(c jwt.MapClaims) { c["sub"] = want.String() })

	var gotID uuid.UUID
	var gotBearer string
	var gotOK bool
	ok := runGuarded(env, token, rec, func(w http.ResponseWriter, r *http.Request) bool {
		gotID, gotBearer, gotOK = jwtauth.CallerID(w, r, rec.ew)
		return gotOK
	})
	if !ok || !gotOK || rec.called {
		t.Fatalf("happy path: ok=%v gotOK=%v ewCalled=%v", ok, gotOK, rec.called)
	}
	if gotID != want {
		t.Fatalf("id = %s, want %s", gotID, want)
	}
	if gotBearer != token {
		t.Fatalf("bearer = %q, want the raw token", gotBearer)
	}
}

func TestCallerID_NoClaimsInContext(t *testing.T) {
	rec := &recordEW{}
	r := httptest.NewRequest(http.MethodGet, "/", nil) // never went through Middleware
	w := httptest.NewRecorder()
	id, bearer, ok := jwtauth.CallerID(w, r, rec.ew)
	if ok || id != uuid.Nil || bearer != "" {
		t.Fatalf("no claims: got (%s, %q, %v), want zero values and false", id, bearer, ok)
	}
	if !rec.called || rec.status != http.StatusUnauthorized || rec.code != "missing_token" ||
		rec.detail != "no validated token in context" {
		t.Fatalf("ew = %+v, want 401 missing_token", rec)
	}
}

// TestCallerID_BadSubject pins the reconciled detail string: collection
// and social used to answer this branch with two different 500
// details ("token subject is not a user id" vs "bad subject"); both
// now answer with the former. auth mints every subject as a uuid, so
// this branch is not known to be reachable in production, but the
// wording still had to converge on one string.
func TestCallerID_BadSubject(t *testing.T) {
	env := newGuardEnv(t)
	rec := &recordEW{}
	token := mint(t, env.kid, env.priv, func(c jwt.MapClaims) { c["sub"] = "not-a-uuid" })

	var gotOK bool
	ok := runGuarded(env, token, rec, func(w http.ResponseWriter, r *http.Request) bool {
		_, _, gotOK = jwtauth.CallerID(w, r, rec.ew)
		return gotOK
	})
	if ok || gotOK {
		t.Fatal("non-uuid subject must not pass CallerID")
	}
	if !rec.called || rec.status != http.StatusInternalServerError || rec.code != "internal" ||
		rec.detail != "token subject is not a user id" {
		t.Fatalf("ew = %+v, want 500 internal with the reconciled detail", rec)
	}
}
