// Package jwtauthtest mints real, validator-accepted vgkeep access
// tokens for tests: an in-process Ed25519 key and JWKS server stand in
// for the auth service, so a suite can drive its real jwtauth
// middleware without one running. It replaces four services'
// independently hand-rolled key/JWKS/mint boilerplate with one
// implementation.
package jwtauthtest

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/jwtauth"
)

// Issuer and Audience are the iss/aud pair every minted token and
// Validator in this package agree on. Fixed, not a NewEnv parameter:
// every real adopter already hardcodes this same pair (the auth
// service is the only real issuer, and it mints for one audience).
const (
	Issuer   = "vgkeep-auth"
	Audience = "vgkeep"
)

// kid identifies Env's one signing key in the JWKS document it serves.
// Its value is only ever compared against itself, never asserted on by
// a caller, so any fixed string works.
const kid = "test-key"

// Env is an in-process JWKS server plus the Ed25519 key it serves:
// real signatures, verified through the real jwtauth.Validator, no
// auth service required. Cheap to build (an in-process httptest
// server, not a container), so unlike valkeytest's per-suite
// singleton, a fresh Env per test is the norm.
type Env struct {
	// Validator is wired to this Env's JWKS server and the
	// Issuer/Audience pair above: hand it to jwtauth's middleware, or
	// call Validate directly.
	Validator *jwtauth.Validator

	priv ed25519.PrivateKey
}

// NewEnv generates an Ed25519 key, serves its public half as a JWKS
// document from an httptest server for the life of t, and returns an
// Env whose Validator accepts tokens Token and ServiceToken mint.
func NewEnv(t *testing.T) *Env {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jwks, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "OKP", "crv": "Ed25519", "kid": kid,
			"x": base64.RawURLEncoding.EncodeToString(pub),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(jwks)
	}))
	t.Cleanup(srv.Close)
	return &Env{
		Validator: jwtauth.NewValidator(srv.URL, Issuer, Audience),
		priv:      priv,
	}
}

// Token mints a valid access token for sub carrying roles. Called with
// no roles it mints a roleless token (zero variadic args is a nil
// slice, not []string{"user"}): callers that want a default role pick
// one themselves, since which role - if any - a bare call should
// assume differs by adopter.
func (e *Env) Token(t *testing.T, sub string, roles ...string) string {
	t.Helper()
	now := time.Now()
	return e.sign(t, jwt.MapClaims{
		"sub": sub, "iss": Issuer, "aud": Audience,
		"jti": uuid.NewString(), "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"roles": roles,
	})
}

// ServiceToken mints a valid access token carrying token_use=service
// (no roles claim at all) for sub, mirroring how auth's internal
// service-token endpoint mints a machine credential - e.g. for the
// catalog-refresh CronJob - that requireAdminOrService-style guards
// admit alongside an admin bearer.
func (e *Env) ServiceToken(t *testing.T, sub string) string {
	t.Helper()
	now := time.Now()
	return e.sign(t, jwt.MapClaims{
		"sub": sub, "iss": Issuer, "aud": Audience,
		"jti": uuid.NewString(), "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"token_use": jwtauth.TokenUseService,
	})
}

func (e *Env) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(e.priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
