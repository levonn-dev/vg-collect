package jwtauth_test

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/levonn-dev/vg-collect/libs/go/jwtauth"
)

const (
	testIssuer   = "vg-collect-auth"
	testAudience = "vg-collect"
)

func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func jwksJSON(kid string, pub ed25519.PublicKey) []byte {
	b, _ := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "kid": kid,
		"x": base64.RawURLEncoding.EncodeToString(pub),
	}}})
	return b
}

func mint(t *testing.T, kid string, priv ed25519.PrivateKey, mutate func(jwt.MapClaims)) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub": "user-1", "iss": testIssuer, "aud": testAudience,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"jti": "jti-1", "roles": []string{"user"},
	}
	if mutate != nil {
		mutate(claims)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestValidate_HappyPath(t *testing.T) {
	pub, priv := genKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksJSON("k1", pub))
	}))
	defer srv.Close()
	v := jwtauth.NewValidator(srv.URL, testIssuer, testAudience)
	claims, err := v.Validate(t.Context(), mint(t, "k1", priv, nil))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if claims.Subject != "user-1" || claims.JTI != "jti-1" || len(claims.Roles) != 1 || claims.Roles[0] != "user" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestValidate_Expired(t *testing.T) {
	pub, priv := genKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksJSON("k1", pub))
	}))
	defer srv.Close()
	v := jwtauth.NewValidator(srv.URL, testIssuer, testAudience)
	tok := mint(t, "k1", priv, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() })
	if _, err := v.Validate(t.Context(), tok); err == nil {
		t.Fatal("want error for expired token")
	}
}

func TestValidate_UnknownKidTriggersRefetch(t *testing.T) {
	pub1, _ := genKey(t)
	pub2, priv2 := genKey(t)
	var rotated atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if rotated.Load() {
			_, _ = w.Write(jwksJSON("k2", pub2))
			return
		}
		_, _ = w.Write(jwksJSON("k1", pub1))
	}))
	defer srv.Close()
	v := jwtauth.NewValidatorWithRefetchInterval(srv.URL, testIssuer, testAudience, 0)
	rotated.Store(true)
	if _, err := v.Validate(t.Context(), mint(t, "k2", priv2, nil)); err != nil {
		t.Fatalf("rotation not picked up: %v", err)
	}
}

func TestMiddleware_NoTokenAndRoles(t *testing.T) {
	pub, priv := genKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksJSON("k1", pub))
	}))
	defer srv.Close()
	v := jwtauth.NewValidator(srv.URL, testIssuer, testAudience)
	ew := func(w http.ResponseWriter, _ *http.Request, status int, _, _ string) { w.WriteHeader(status) }
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })

	authed := jwtauth.Middleware(v, ew)(ok)
	w := httptest.NewRecorder()
	authed.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 401 {
		t.Fatalf("no token: status = %d, want 401", w.Code)
	}

	admin := jwtauth.Middleware(v, ew)(jwtauth.RequireRole("admin", ew)(ok))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+mint(t, "k1", priv, nil)) // roles: [user]
	w = httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("user hitting admin route: status = %d, want 403", w.Code)
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+mint(t, "k1", priv, func(c jwt.MapClaims) { c["roles"] = []string{"admin"} }))
	w = httptest.NewRecorder()
	admin.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("admin: status = %d, want 200", w.Code)
	}
}

func TestValidate_RejectsAlgConfusion(t *testing.T) {
	pub, _ := genKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksJSON("k1", pub))
	}))
	defer srv.Close()
	v := jwtauth.NewValidator(srv.URL, testIssuer, testAudience)

	claims := jwt.MapClaims{
		"sub": "user-1", "iss": testIssuer, "aud": testAudience,
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	}

	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	hs.Header["kid"] = "k1"
	hsTok, err := hs.SignedString([]byte(pub)) // public-key bytes as HMAC secret
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate(t.Context(), hsTok); err == nil {
		t.Fatal("HS256 token with public-key secret must be rejected")
	}

	none := jwt.NewWithClaims(jwt.SigningMethodNone, claims)
	none.Header["kid"] = "k1"
	noneTok, err := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Validate(t.Context(), noneTok); err == nil {
		t.Fatal("alg=none token must be rejected")
	}
}

func TestValidate_WrongIssuerOrAudience(t *testing.T) {
	pub, priv := genKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(jwksJSON("k1", pub))
	}))
	defer srv.Close()
	v := jwtauth.NewValidator(srv.URL, testIssuer, testAudience)

	cases := map[string]func(jwt.MapClaims){
		"wrong issuer":     func(c jwt.MapClaims) { c["iss"] = "evil" },
		"missing issuer":   func(c jwt.MapClaims) { delete(c, "iss") },
		"wrong audience":   func(c jwt.MapClaims) { c["aud"] = "other" },
		"missing audience": func(c jwt.MapClaims) { delete(c, "aud") },
	}
	for name, mutate := range cases {
		if _, err := v.Validate(t.Context(), mint(t, "k1", priv, mutate)); err == nil {
			t.Fatalf("%s: want rejection", name)
		}
	}
}

func TestKeyCache_ThrottlesUnknownKidFlood(t *testing.T) {
	pub, _ := genKey(t)
	var fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		_, _ = w.Write(jwksJSON("k1", pub))
	}))
	defer srv.Close()
	_, priv2 := genKey(t)
	v := jwtauth.NewValidatorWithRefetchInterval(srv.URL, testIssuer, testAudience, time.Hour)
	for range 5 {
		if _, err := v.Validate(t.Context(), mint(t, "unknown-kid", priv2, nil)); err == nil {
			t.Fatal("unknown kid must fail")
		}
	}
	if got := fetches.Load(); got != 1 {
		t.Fatalf("fetches = %d, want exactly 1 within the throttle window", got)
	}
}

func TestValidate_MalformedJWKS(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"non-200":      func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) },
		"invalid json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{nope")) },
		"bad entries": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"k1"},{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"!!!"}]}`))
		},
	}
	_, priv := genKey(t)
	for name, handler := range cases {
		srv := httptest.NewServer(handler)
		v := jwtauth.NewValidator(srv.URL, testIssuer, testAudience)
		if _, err := v.Validate(t.Context(), mint(t, "k1", priv, nil)); err == nil {
			t.Fatalf("%s: want clean error", name)
		}
		srv.Close()
	}
}
