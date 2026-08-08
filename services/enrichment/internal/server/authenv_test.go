// White-box test package (like the bff's server tests): handler tests
// reach the unexported now seam for staleness math.
package server

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

// authEnv is an in-process JWKS + signer: handler tests exercise the
// real validator instead of stubbing authentication.
type authEnv struct {
	srv  *httptest.Server
	priv ed25519.PrivateKey
	kid  string
}

func newAuthEnv(t *testing.T) *authEnv {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	kid := "test-kid-1"
	jwks, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "OKP",
			"crv": "Ed25519",
			"kid": kid,
			"x":   base64.RawURLEncoding.EncodeToString(pub),
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
	return &authEnv{srv: srv, priv: priv, kid: kid}
}

// token mints a valid access JWT for sub with the given roles.
func (a *authEnv) token(t *testing.T, sub string, roles []string) string {
	t.Helper()
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"sub":   sub,
		"roles": roles,
		"iss":   "vgkeep-auth",
		"aud":   "vgkeep",
		"jti":   uuid.NewString(),
		"iat":   now.Unix(),
		"exp":   now.Add(5 * time.Minute).Unix(),
	})
	tok.Header["kid"] = a.kid
	signed, err := tok.SignedString(a.priv)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func (a *authEnv) validator() *jwtauth.Validator {
	return jwtauth.NewValidator(a.srv.URL, "vgkeep-auth", "vgkeep")
}

// serviceToken mints a valid access JWT carrying token_use=service (no
// roles) for sub, mirroring how auth's internal service-token endpoint
// mints a machine credential for the catalog-refresh CronJob.
func (a *authEnv) serviceToken(t *testing.T, sub string) string {
	t.Helper()
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"sub":       sub,
		"token_use": "service",
		"iss":       "vgkeep-auth",
		"aud":       "vgkeep",
		"jti":       uuid.NewString(),
		"iat":       now.Unix(),
		"exp":       now.Add(5 * time.Minute).Unix(),
	})
	tok.Header["kid"] = a.kid
	signed, err := tok.SignedString(a.priv)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
