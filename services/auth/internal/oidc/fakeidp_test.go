package oidc_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// fakeIDP is an httptest OIDC provider: discovery, JWKS (RSA), and a
// token endpoint that redeems pre-registered codes for ID tokens.
type fakeIDP struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey
	kid string

	discoveryCalls atomic.Int32
	lastTokenForm  url.Values

	// knobs
	discoveryStatus int           // 0 means 200
	tokenStatus     int           // 0 means 200
	tokenRawBody    string        // non-empty overrides the JSON response
	tokenDelay      time.Duration // simulate a slow provider
	serveEmptyJWKS  bool          // serve {"keys":[]} to simulate provider degradation
	codes           map[string]jwt.MapClaims
}

func newFakeIDP(t *testing.T) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIDP{t: t, key: key, kid: "idp-key-1", codes: map[string]jwt.MapClaims{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", f.discovery)
	mux.HandleFunc("GET /jwks", f.jwks)
	mux.HandleFunc("POST /token", f.token)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIDP) issuer() string { return f.srv.URL }

func (f *fakeIDP) discovery(w http.ResponseWriter, _ *http.Request) {
	f.discoveryCalls.Add(1)
	if f.discoveryStatus != 0 {
		w.WriteHeader(f.discoveryStatus)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{
		"issuer":                 f.srv.URL,
		"authorization_endpoint": f.srv.URL + "/authorize",
		"token_endpoint":         f.srv.URL + "/token",
		"jwks_uri":               f.srv.URL + "/jwks",
	})
}

func (f *fakeIDP) jwks(w http.ResponseWriter, _ *http.Request) {
	if f.serveEmptyJWKS {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{}})
		return
	}
	pub := &f.key.PublicKey
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": f.kid, "alg": "RS256", "use": "sig",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (f *fakeIDP) token(w http.ResponseWriter, r *http.Request) {
	if f.tokenDelay > 0 {
		time.Sleep(f.tokenDelay)
	}
	_ = r.ParseForm()
	f.lastTokenForm = r.PostForm
	if f.tokenStatus != 0 {
		w.WriteHeader(f.tokenStatus)
		return
	}
	if f.tokenRawBody != "" {
		_, _ = w.Write([]byte(f.tokenRawBody))
		return
	}
	claims, ok := f.codes[r.PostForm.Get("code")]
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{
		"id_token": f.mint(claims), "access_token": "opaque", "token_type": "Bearer",
	})
}

// registerCode makes the token endpoint redeem code for an ID token
// with these claims (defaults filled for iss/aud/exp unless preset).
func (f *fakeIDP) registerCode(code, clientID, nonce string, claims jwt.MapClaims) {
	merged := jwt.MapClaims{
		"iss": f.srv.URL, "aud": clientID, "nonce": nonce,
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	}
	for k, v := range claims {
		merged[k] = v
	}
	f.codes[code] = merged
}

func (f *fakeIDP) mint(claims jwt.MapClaims) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = f.kid
	s, err := tok.SignedString(f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}

// mintWithKid signs with the real key but stamps an arbitrary kid, to
// simulate a token whose kid is absent from the served JWKS.
func (f *fakeIDP) mintWithKid(claims jwt.MapClaims, kid string) string {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return s
}
