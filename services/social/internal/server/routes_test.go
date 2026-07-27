package server_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
	"github.com/levonn-dev/vg-collect/services/social/internal/server"
)

// authEnv is an in-process token mint + JWKS: real Ed25519 signatures
// through the real jwtauth middleware, no auth service needed.
type authEnv struct {
	v   *jwtauth.Validator
	key ed25519.PrivateKey
	kid string
}

func newAuthEnv(t *testing.T) authEnv {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "test-key"
	jwks := fmt.Sprintf(`{"keys":[{"kty":"OKP","crv":"Ed25519","kid":%q,"x":%q}]}`,
		kid, base64.RawURLEncoding.EncodeToString(pub))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jwks))
	}))
	t.Cleanup(srv.Close)
	return authEnv{
		v:   jwtauth.NewValidatorWithRefetchInterval(srv.URL, "vg-collect-auth", "vg-collect", 0),
		key: priv,
		kid: kid,
	}
}

// token mints a valid access token for sub (a user uuid string).
func (a authEnv) token(t *testing.T, sub string, roles ...string) string {
	t.Helper()
	if roles == nil {
		roles = []string{"user"}
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"sub": sub, "iss": "vg-collect-auth", "aud": "vg-collect",
		"jti": uuid.NewString(), "exp": time.Now().Add(5 * time.Minute).Unix(),
		"roles": roles,
	})
	tok.Header["kid"] = a.kid
	s, err := tok.SignedString(a.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// testLogger discards output: the valid-token route loop in
// TestUnitAPIRoutesRequireJWT deliberately drives handlers with nil
// collaborators, and httpkit.Recover logs those panics at ERROR - a
// real logger would spam -v output with expected noise.
func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// newUnitServer mounts Handlers behind the real router + middleware.
// Collaborators may be nil while a test exercises only routing/auth.
func newUnitServer(t *testing.T, st server.Store, col server.Collection, users server.Users) (*httptest.Server, authEnv) {
	t.Helper()
	a := newAuthEnv(t)
	h := server.New(st, col, users, server.Options{
		Logger: testLogger(), CapComments: 50, CapFollows: 100, CapLikes: 200,
	})
	router := server.NewRouter(h, a.v, testLogger(), func(context.Context) error { return nil })
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return srv, a
}

// do issues a request with an optional bearer and returns the
// response. body is JSON-encoded when non-nil.
func do(t *testing.T, method, url, bearer string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			t.Fatal(err)
		}
		r = buf
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
	if resp.StatusCode != status {
		t.Fatalf("status: got %d, want %d", resp.StatusCode, status)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type: %q", ct)
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Code != code {
		t.Fatalf("code: got %q, want %q", p.Code, code)
	}
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
		Logger: testLogger(), CapComments: 50, CapFollows: 100, CapLikes: 200,
	})
	router := server.NewRouter(h, a.v, testLogger(),
		func(context.Context) error { return errors.New("not ready") })
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
		{http.MethodGet, "/comments/by-ids"},
		{http.MethodDelete, "/comments/" + uuid.NewString()},
		{http.MethodPost, "/events/shelf-published"},
		{http.MethodGet, "/explore/top-shelves"},
		{http.MethodGet, "/feed"},
		{http.MethodDelete, "/follows/" + uuid.NewString()},
		{http.MethodPut, "/follows/" + uuid.NewString()},
		{http.MethodDelete, "/likes/" + uuid.NewString()},
		{http.MethodPut, "/likes/" + uuid.NewString()},
		{http.MethodGet, "/profiles/" + uuid.NewString() + "/summary"},
		{http.MethodGet, "/shelves/summary"},
		{http.MethodGet, "/shelves/" + uuid.NewString() + "/comments"},
		{http.MethodPost, "/shelves/" + uuid.NewString() + "/comments"},
		{http.MethodDelete, "/user-data"},
	}
	for _, p := range paths {
		resp := do(t, p.method, srv.URL+p.path, "", nil)
		wantProblem(t, resp, http.StatusUnauthorized, "missing_token")
	}
	// A valid token passes the gate: whatever the handler answers, the
	// middleware must not (401/403 would mean auth swallowed it). This
	// assertion survives the skeleton handlers being replaced.
	for _, p := range paths {
		resp := do(t, p.method, srv.URL+p.path, a.token(t, uuid.NewString()), nil)
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			t.Fatalf("%s %s: a valid token must pass the middleware, got %d", p.method, p.path, resp.StatusCode)
		}
	}
}

func TestUnitBadParamIsProblemJSON(t *testing.T) {
	srv, a := newUnitServer(t, nil, nil, nil)
	resp := do(t, http.MethodPut, srv.URL+"/follows/not-a-uuid", a.token(t, uuid.NewString()), nil)
	wantProblem(t, resp, http.StatusBadRequest, "invalid_param")
}

// stubErrMeterProvider hands out a meter that refuses every counter
// registration this service performs; the noop embeds satisfy the
// rest of the interfaces.
type stubErrMeterProvider struct{ noop.MeterProvider }

func (stubErrMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return stubErrMeter{}
}

type stubErrMeter struct{ noop.Meter }

func (stubErrMeter) Int64Counter(string, ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	return nil, errors.New("registration refused")
}

// TestUnitNew_NilLoggerDoesNotPanic pins the constructor's
// tolerate-nil idiom (shared across services): a caller that leaves
// Options.Logger nil must not crash New, whose counter registration
// otherwise logs straight through it. Every registration is forced to
// fail, so New actually reaches opts.Logger.Error; without the
// nil-logger guard this call would panic on the nil receiver.
func TestUnitNew_NilLoggerDoesNotPanic(t *testing.T) {
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(stubErrMeterProvider{})
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	h := server.New(nil, nil, nil, server.Options{})
	if h == nil {
		t.Fatal("New returned nil")
	}
}
