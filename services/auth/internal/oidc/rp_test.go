package oidc_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/levonn-dev/vgkeep/services/auth/internal/oidc"
)

func newRP(t *testing.T, f *fakeIDP, hc *http.Client) *oidc.RP {
	t.Helper()
	return oidc.NewRP(oidc.RPConfig{
		Name:         "fake",
		IssuerURL:    f.issuer(),
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		RedirectURL:  "https://app.example/api/auth/callback",
		Scopes:       []string{"openid", "email", "profile"},
		ExtraAuthParams: url.Values{
			"claims": []string{`{"id_token":{"email":null}}`},
		},
	}, hc)
}

func newRPRefetch(t *testing.T, f *fakeIDP, interval time.Duration) *oidc.RP {
	t.Helper()
	return oidc.NewRPWithRefetchInterval(oidc.RPConfig{
		Name:         "fake",
		IssuerURL:    f.issuer(),
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		RedirectURL:  "https://app.example/api/auth/callback",
		Scopes:       []string{"openid", "email", "profile"},
	}, nil, interval)
}

func TestRandomTokenAndPKCE(t *testing.T) {
	if a, b := oidc.RandomToken(), oidc.RandomToken(); a == b || len(a) != 43 {
		t.Fatalf("RandomToken broken: %q %q", a, b)
	}
	v, c := oidc.NewPKCE()
	if v == "" || c == "" || v == c {
		t.Fatalf("NewPKCE: %q %q", v, c)
	}
}

func TestAuthorizeURL(t *testing.T) {
	f := newFakeIDP(t)
	p := newRP(t, f, nil)

	raw, err := p.AuthorizeURL(context.Background(), "st1", "n1", "ch1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, f.issuer()+"/authorize?") {
		t.Fatalf("authorize endpoint wrong: %s", raw)
	}
	q := u.Query()
	want := map[string]string{
		"response_type":         "code",
		"client_id":             "client-1",
		"redirect_uri":          "https://app.example/api/auth/callback",
		"scope":                 "openid email profile",
		"state":                 "st1",
		"nonce":                 "n1",
		"code_challenge":        "ch1",
		"code_challenge_method": "S256",
		"claims":                `{"id_token":{"email":null}}`,
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), v)
		}
	}

	// Discovery is lazy and cached: a second call must not refetch.
	if _, err := p.AuthorizeURL(context.Background(), "st2", "n2", "ch2"); err != nil {
		t.Fatal(err)
	}
	if n := f.discoveryCalls.Load(); n != 1 {
		t.Fatalf("discovery fetched %d times, want 1", n)
	}
}

func TestDiscovery_FailureIsRetriedNextCall(t *testing.T) {
	f := newFakeIDP(t)
	f.discoveryStatus = http.StatusInternalServerError
	p := newRP(t, f, nil)

	if _, err := p.AuthorizeURL(context.Background(), "s", "n", "c"); err == nil {
		t.Fatal("want discovery error")
	}
	f.discoveryStatus = 0 // provider recovers; only successes are cached
	if _, err := p.AuthorizeURL(context.Background(), "s", "n", "c"); err != nil {
		t.Fatalf("recovered discovery still failing: %v", err)
	}
}

func TestDiscovery_IssuerMismatchRejected(t *testing.T) {
	f := newFakeIDP(t)
	p := oidc.NewRP(oidc.RPConfig{
		Name: "fake", IssuerURL: f.issuer() + "/not-the-issuer",
		ClientID: "c", ClientSecret: "s", RedirectURL: "https://x",
		Scopes: []string{"openid"},
	}, nil)
	// The discovery document lives under the configured issuer path, so
	// the fetch itself 404s; a mix-up where the doc loads but declares a
	// different issuer is covered by Exchange's issuer check below.
	if _, err := p.AuthorizeURL(context.Background(), "s", "n", "c"); err == nil {
		t.Fatal("want error for issuer mismatch")
	}
}

func TestExchange_HappyPath(t *testing.T) {
	f := newFakeIDP(t)
	p := newRP(t, f, nil)
	f.registerCode("code-1", "client-1", "n1", jwt.MapClaims{
		"sub": "prov-sub-1", "email": "a@example.com", "email_verified": true,
		"name": "Alice Provider", "picture": "https://img.example/a.png",
	})

	claims, err := p.Exchange(context.Background(), "code-1", "ver-1", "n1")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "prov-sub-1" || claims.Email != "a@example.com" ||
		!claims.EmailVerified || claims.DisplayName != "Alice Provider" ||
		claims.AvatarURL != "https://img.example/a.png" {
		t.Fatalf("claims = %+v", claims)
	}

	// The exchange must be a spec-shaped client_secret_post + PKCE call.
	form := f.lastTokenForm
	for k, v := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "code-1",
		"redirect_uri":  "https://app.example/api/auth/callback",
		"code_verifier": "ver-1",
		"client_id":     "client-1",
		"client_secret": "secret-1",
	} {
		if form.Get(k) != v {
			t.Fatalf("token form %s = %q, want %q", k, form.Get(k), v)
		}
	}
}

func TestExchange_DisplayNameFallbacks(t *testing.T) {
	f := newFakeIDP(t)
	p := newRP(t, f, nil)

	// Twitch shape: preferred_username, no name.
	f.registerCode("c-twitch", "client-1", "n", jwt.MapClaims{
		"sub": "s1", "email": "tw@example.com", "email_verified": true,
		"preferred_username": "twitchy",
	})
	got, err := p.Exchange(context.Background(), "c-twitch", "v", "n")
	if err != nil || got.DisplayName != "twitchy" {
		t.Fatalf("preferred_username fallback: %+v, %v", got, err)
	}

	// Nothing but an email: local part.
	f.registerCode("c-bare", "client-1", "n", jwt.MapClaims{
		"sub": "s2", "email": "bare@example.com", "email_verified": true,
	})
	got, err = p.Exchange(context.Background(), "c-bare", "v", "n")
	if err != nil || got.DisplayName != "bare" {
		t.Fatalf("email-local fallback: %+v, %v", got, err)
	}
}

func TestExchange_VerificationFailures(t *testing.T) {
	f := newFakeIDP(t)
	p := newRP(t, f, nil)

	f.registerCode("bad-nonce", "client-1", "expected", jwt.MapClaims{"sub": "s"})
	if _, err := p.Exchange(context.Background(), "bad-nonce", "v", "different"); err == nil {
		t.Fatal("nonce mismatch accepted")
	}

	f.registerCode("bad-aud", "other-client", "n", jwt.MapClaims{"sub": "s"})
	if _, err := p.Exchange(context.Background(), "bad-aud", "v", "n"); err == nil {
		t.Fatal("wrong audience accepted")
	}

	f.registerCode("bad-iss", "client-1", "n", jwt.MapClaims{"sub": "s", "iss": "https://evil.example"})
	if _, err := p.Exchange(context.Background(), "bad-iss", "v", "n"); err == nil {
		t.Fatal("wrong issuer accepted")
	}

	f.registerCode("expired", "client-1", "n", jwt.MapClaims{
		"sub": "s", "exp": time.Now().Add(-time.Hour).Unix(),
	})
	if _, err := p.Exchange(context.Background(), "expired", "v", "n"); err == nil {
		t.Fatal("expired ID token accepted")
	}

	f.registerCode("no-sub", "client-1", "n", jwt.MapClaims{"email": "x@example.com"})
	if _, err := p.Exchange(context.Background(), "no-sub", "v", "n"); err == nil {
		t.Fatal("ID token without sub accepted")
	}
}

func TestExchange_BadSignatureRejected(t *testing.T) {
	f := newFakeIDP(t)
	p := newRP(t, f, nil)

	// Sign with a key the JWKS does not serve.
	rogue, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	realKey := f.key
	f.key = rogue
	f.registerCode("forged", "client-1", "n", jwt.MapClaims{"sub": "s"})
	idToken := f.mint(f.codes["forged"]) // minted with the rogue key, kid unchanged
	f.key = realKey
	f.tokenRawBody = `{"id_token":"` + idToken + `"}`

	if _, err := p.Exchange(context.Background(), "forged", "v", "n"); err == nil {
		t.Fatal("forged signature accepted")
	}
}

func TestExchange_ProviderFailureModes(t *testing.T) {
	ctx := context.Background()

	t.Run("429", func(t *testing.T) {
		f := newFakeIDP(t)
		p := newRP(t, f, nil)
		f.tokenStatus = http.StatusTooManyRequests
		var pe *oidc.ProviderError
		if _, err := p.Exchange(ctx, "c", "v", "n"); !errors.As(err, &pe) || pe.Status != 429 {
			t.Fatalf("want ProviderError{429}, got %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		f := newFakeIDP(t)
		f.tokenDelay = 300 * time.Millisecond
		p := newRP(t, f, &http.Client{Timeout: 50 * time.Millisecond})
		var pe *oidc.ProviderError
		if _, err := p.Exchange(ctx, "c", "v", "n"); !errors.As(err, &pe) {
			t.Fatalf("want ProviderError for timeout, got %v", err)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		f := newFakeIDP(t)
		p := newRP(t, f, nil)
		f.tokenRawBody = "{not json"
		if _, err := p.Exchange(ctx, "c", "v", "n"); err == nil {
			t.Fatal("malformed token response accepted")
		}
	})

	t.Run("missing id_token", func(t *testing.T) {
		f := newFakeIDP(t)
		p := newRP(t, f, nil)
		f.tokenRawBody = `{"access_token":"only"}`
		if _, err := p.Exchange(ctx, "c", "v", "n"); err == nil {
			t.Fatal("response without id_token accepted")
		}
	})
}

func TestJWKS_RotatedKidTriggersRefetch(t *testing.T) {
	f := newFakeIDP(t)
	// Throttle disabled (0): the rotated kid is unknown, so the cache
	// refetches immediately instead of waiting out the default window.
	p := newRPRefetch(t, f, 0)

	f.registerCode("c1", "client-1", "n", jwt.MapClaims{"sub": "s"})
	if _, err := p.Exchange(context.Background(), "c1", "v", "n"); err != nil {
		t.Fatal(err)
	}

	// Provider rotates its key (new kid): the cache misses and refetches.
	rotated, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.key, f.kid = rotated, "idp-key-2"
	f.registerCode("c2", "client-1", "n", jwt.MapClaims{"sub": "s"})
	if _, err := p.Exchange(context.Background(), "c2", "v", "n"); err != nil {
		t.Fatalf("rotation not handled: %v", err)
	}
}

func TestJWKS_EmptyRefetchDoesNotEvictGoodKeys(t *testing.T) {
	f := newFakeIDP(t)
	p := newRPRefetch(t, f, 0) // throttle disabled so refetch is attempted

	// Prime the cache with the provider's real key via a normal exchange.
	f.registerCode("c1", "client-1", "n", jwt.MapClaims{"sub": "s"})
	if _, err := p.Exchange(context.Background(), "c1", "v", "n"); err != nil {
		t.Fatal(err)
	}

	// Provider goes briefly degraded: its JWKS now serves no keys. A
	// token with an unknown kid forces a refetch that returns empty.
	f.serveEmptyJWKS = true
	bad := f.mintWithKid(jwt.MapClaims{
		"iss": f.issuer(), "aud": "client-1", "nonce": "n",
		"exp": time.Now().Add(time.Hour).Unix(), "sub": "s",
	}, "rotated-unknown-kid")
	f.tokenRawBody = `{"id_token":"` + bad + `"}`
	if _, err := p.Exchange(context.Background(), "c-bad", "v", "n"); err == nil {
		t.Fatal("unknown kid against an empty JWKS should fail")
	}

	// The good key must survive the empty refetch: a token with the
	// original kid still verifies, served from the preserved cache
	// (the JWKS is still empty, so this proves no eviction occurred).
	f.tokenRawBody = ""
	f.registerCode("c2", "client-1", "n", jwt.MapClaims{"sub": "s"})
	if _, err := p.Exchange(context.Background(), "c2", "v", "n"); err != nil {
		t.Fatalf("good cached key was evicted by an empty refetch: %v", err)
	}
}
