// Tests for login, callback, logout, and provider-link handlers.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
)

func TestLoginProviderRedirects(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{
		start: func(_ context.Context, p string) (string, error) {
			if p != "google" {
				t.Errorf("provider = %q", p)
			}
			return "https://idp.example/authorize?state=s", nil
		},
	})
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/login?provider=google", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://idp.example/authorize?state=s" {
		t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	st := findCookie(rec.Result().Cookies(), session.StateCookieName)
	if st == nil || st.Value != "s" || !st.HttpOnly {
		t.Fatalf("state cookie = %+v", st)
	}
}

func TestLoginDevSetsCookieAndGoesHome(t *testing.T) {
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	h := newTestHandlers(t, newStubCache(), &stubAuth{
		dev: func(_ context.Context, user string) (authclient.TokenPair, error) {
			if user != "alice" {
				t.Errorf("user = %q", user)
			}
			return authclient.TokenPair{AccessToken: access, RefreshToken: "r1",
				ExpiresIn: 300, RefreshExpiresIn: 2000}, nil
		},
	})
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/login?provider=dev&user=alice", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != session.CookieName || cs[0].MaxAge != 2000 || !cs[0].HttpOnly {
		t.Fatalf("cookies = %+v", cs)
	}
	if opened, err := h.codec.Open(cs[0].Value); err != nil || opened.RefreshToken != "r1" {
		t.Fatalf("cookie content: %+v err=%v", opened, err)
	}
}

func TestLoginFailureRedirectsToLoginPage(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{authclient.ErrLoginFailed, "login_failed"},
		{authclient.ErrProviderError, "provider_error"},
		{errors.New("boom"), "login_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.code+"_"+tc.err.Error(), func(t *testing.T) {
			h := newTestHandlers(t, newStubCache(), &stubAuth{
				start: func(context.Context, string) (string, error) { return "", tc.err },
			})
			rec := httptest.NewRecorder()
			newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/login?provider=google", nil))
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?error="+tc.code {
				t.Errorf("%v: code=%d location=%q", tc.err, rec.Code, rec.Header().Get("Location"))
			}
		})
	}
}

func TestCallbackSuccess(t *testing.T) {
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	h := newTestHandlers(t, newStubCache(), &stubAuth{
		callback: func(_ context.Context, code, state string) (authclient.TokenPair, error) {
			if code != "c1" || state != "s1" {
				t.Errorf("code=%q state=%q", code, state)
			}
			return authclient.TokenPair{AccessToken: access, RefreshToken: "r1",
				ExpiresIn: 300, RefreshExpiresIn: 2000}, nil
		},
	})
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, oauthStateReq(t, h, "/api/auth/callback?code=c1&state=s1"))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestCallbackFailures(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{authclient.ErrLoginFailed, "login_failed"},
		{authclient.ErrEmailUnverified, "email_unverified"},
		{authclient.ErrProviderError, "provider_error"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			h := newTestHandlers(t, newStubCache(), &stubAuth{
				callback: func(context.Context, string, string) (authclient.TokenPair, error) {
					return authclient.TokenPair{}, tc.err
				},
			})
			rec := httptest.NewRecorder()
			newRouterFor(t, h).ServeHTTP(rec, oauthStateReq(t, h, "/api/auth/callback?code=c&state=s"))
			if rec.Header().Get("Location") != "/login?error="+tc.code {
				t.Errorf("%v: location=%q", tc.err, rec.Header().Get("Location"))
			}
		})
	}
	// Missing params never reach the auth service.
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil))
	if rec.Header().Get("Location") != "/login?error=login_failed" {
		t.Errorf("missing params: location=%q", rec.Header().Get("Location"))
	}
}

func TestCallbackMissingOrMismatchedStateCookie(t *testing.T) {
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	newHandlers := func() *Handlers {
		return newTestHandlers(t, newStubCache(), &stubAuth{
			callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{AccessToken: access, RefreshToken: "r1",
					ExpiresIn: 300, RefreshExpiresIn: 2000}, nil
			},
		})
	}

	t.Run("no cookie", func(t *testing.T) {
		h := newHandlers()
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c1&state=s1", nil))
		if rec.Header().Get("Location") != "/login?error=login_failed" {
			t.Fatalf("location=%q", rec.Header().Get("Location"))
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("no session must be sealed: %+v", rec.Result().Cookies())
		}
	})

	t.Run("mismatched cookie", func(t *testing.T) {
		h := newHandlers()
		req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c1&state=s1", nil)
		req.AddCookie(h.codec.StateCookie("wrong-state", 600))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, req)
		if rec.Header().Get("Location") != "/login?error=login_failed" {
			t.Fatalf("location=%q", rec.Header().Get("Location"))
		}
		for _, c := range rec.Result().Cookies() {
			if c.Name == session.CookieName {
				t.Fatalf("no session must be sealed: %+v", rec.Result().Cookies())
			}
		}
	})
}

func TestCallbackLinkOutcomes(t *testing.T) {
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	google := "google"

	t.Run("link_success_redirects_to_account_with_provider", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuth{
			callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{AccessToken: access, RefreshToken: "r1",
					ExpiresIn: 300, RefreshExpiresIn: 2000, LinkedProvider: &google}, nil
			},
		})
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, oauthStateReq(t, h, "/api/auth/callback?code=c1&state=s1"))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?linked=google" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		cs := rec.Result().Cookies()
		if len(cs) != 2 {
			t.Fatalf("cookies = %+v, want session + cleared state", cs)
		}
		sessCookie, clearedState := findCookie(cs, session.CookieName), findCookie(cs, session.StateCookieName)
		if sessCookie == nil || clearedState == nil || clearedState.MaxAge >= 0 {
			t.Fatalf("cookies = %+v", cs)
		}
		if opened, err := h.codec.Open(sessCookie.Value); err != nil || opened.RefreshToken != "r1" {
			t.Fatalf("cookie content: %+v err=%v", opened, err)
		}
	})

	t.Run("conflict_redirects_without_a_cookie", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuth{
			callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{}, authclient.ErrLinkConflict
			},
		})
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, oauthStateReq(t, h, "/api/auth/callback?code=c&state=s"))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?link_error=conflict" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("conflict must not set a cookie: %+v", rec.Result().Cookies())
		}
	})

	t.Run("email_unverified_redirects_with_link_error", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuth{
			callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{}, authclient.ErrLinkEmailUnverified
			},
		})
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, oauthStateReq(t, h, "/api/auth/callback?code=c&state=s"))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?link_error=email_unverified" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("unverified must not set a cookie: %+v", rec.Result().Cookies())
		}
	})

	t.Run("plain_login_still_redirects_home", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuth{
			callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{AccessToken: access, RefreshToken: "r1",
					ExpiresIn: 300, RefreshExpiresIn: 2000}, nil
			},
		})
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, oauthStateReq(t, h, "/api/auth/callback?code=c&state=s"))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
	})
}

func TestLogout(t *testing.T) {
	fc := newStubCache()
	revoked := ""
	h := newTestHandlers(t, fc, &stubAuth{
		revoke: func(_ context.Context, rt string) error { revoked = rt; return nil },
	})
	access := mintAccess(t, "u1", "j1", time.Now().Add(2*time.Minute))
	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent || !clearedCookie(rec) {
		t.Fatalf("code=%d cleared=%v", rec.Code, clearedCookie(rec))
	}
	if revoked != "r1" {
		t.Fatalf("revoked = %q", revoked)
	}
	if len(fc.denyAdds) != 1 || fc.denyAdds[0][0] != "j1" {
		t.Fatalf("denylist adds = %v", fc.denyAdds)
	}
}

func TestLogoutWithoutSessionIsIdempotent(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{}) // revoke would panic
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil))
	if rec.Code != http.StatusNoContent || !clearedCookie(rec) {
		t.Fatalf("code=%d cleared=%v", rec.Code, clearedCookie(rec))
	}
}

func TestLogoutSurvivesDependencyOutages(t *testing.T) {
	fc := newStubCache()
	fc.err = errors.New("valkey down")
	h := newTestHandlers(t, fc, &stubAuth{
		revoke: func(context.Context, string) error { return errors.New("auth down") },
	})
	access := mintAccess(t, "u1", "j1", time.Now().Add(2*time.Minute))
	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent || !clearedCookie(rec) {
		t.Fatalf("logout must clear the cookie no matter what: code=%d", rec.Code)
	}
}

func TestProviders(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{
		providers: func(context.Context) ([]string, error) { return []string{"google", "dev"}, nil },
	})
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	var body api.Providers
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Providers) != 2 {
		t.Fatalf("body=%v err=%v", body, err)
	}
}

func TestProvidersUpstreamError(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{
		providers: func(context.Context) ([]string, error) { return nil, errors.New("auth down") },
	})
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d", rec.Code)
	}
}
