package oidc_test

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/levonn-dev/vgkeep/services/auth/internal/oidc"
)

func authorizeQuery(t *testing.T, p oidc.Provider) url.Values {
	t.Helper()
	raw, err := p.AuthorizeURL(context.Background(), "s", "n", "c")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

func TestNewGoogle(t *testing.T) {
	f := newFakeIDP(t)
	p := oidc.NewGoogle("gid", "gsecret", "https://app/cb", f.issuer())
	if p.Name() != "google" {
		t.Fatalf("name = %s", p.Name())
	}
	q := authorizeQuery(t, p)
	if q.Get("scope") != "openid email profile" || q.Get("client_id") != "gid" {
		t.Fatalf("google authorize params: %v", q)
	}
	if q.Has("claims") {
		t.Fatal("google must not send a claims parameter")
	}
}

func TestNewTwitch(t *testing.T) {
	f := newFakeIDP(t)
	p := oidc.NewTwitch("tid", "tsecret", "https://app/cb", f.issuer())
	if p.Name() != "twitch" {
		t.Fatalf("name = %s", p.Name())
	}
	q := authorizeQuery(t, p)
	if q.Get("scope") != "openid user:read:email" {
		t.Fatalf("twitch scope = %q", q.Get("scope"))
	}
	// Twitch only puts email/profile fields in the ID token when asked
	// via the OIDC claims request parameter.
	for _, claim := range []string{"email", "email_verified", "preferred_username", "picture"} {
		if !strings.Contains(q.Get("claims"), `"`+claim+`"`) {
			t.Fatalf("twitch claims param missing %s: %s", claim, q.Get("claims"))
		}
	}
}

func TestDevClaims(t *testing.T) {
	for _, handle := range []string{"alice", "bob", "admin"} {
		c, ok := oidc.DevClaims(handle)
		if !ok {
			t.Fatalf("fixture %s missing", handle)
		}
		if c.Subject == "" || c.Email == "" || c.DisplayName == "" || !c.EmailVerified {
			t.Fatalf("fixture %s incomplete: %+v", handle, c)
		}
	}
	if _, ok := oidc.DevClaims("real-person@example.com"); ok {
		t.Fatal("dev provider resolved a non-fixture handle")
	}
}
