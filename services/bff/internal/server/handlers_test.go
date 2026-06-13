package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vg-collect/services/bff/internal/authclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/session"
	"github.com/levonn-dev/vg-collect/services/bff/internal/userclient"
)

// stubAuthFull lets each test override exactly the methods it expects.
type stubAuthFull struct {
	start     func(ctx context.Context, provider string) (string, error)
	callback  func(ctx context.Context, code, state string) (authclient.TokenPair, error)
	dev       func(ctx context.Context, user string) (authclient.TokenPair, error)
	refresh   func(ctx context.Context, rt string) (authclient.TokenPair, error)
	revoke    func(ctx context.Context, rt string) error
	providers func(ctx context.Context) ([]string, error)
}

func (s *stubAuthFull) Start(ctx context.Context, p string) (string, error) {
	if s.start == nil {
		panic("unexpected Start")
	}
	return s.start(ctx, p)
}
func (s *stubAuthFull) Callback(ctx context.Context, c, st string) (authclient.TokenPair, error) {
	if s.callback == nil {
		panic("unexpected Callback")
	}
	return s.callback(ctx, c, st)
}
func (s *stubAuthFull) DevToken(ctx context.Context, u string) (authclient.TokenPair, error) {
	if s.dev == nil {
		panic("unexpected DevToken")
	}
	return s.dev(ctx, u)
}
func (s *stubAuthFull) Refresh(ctx context.Context, rt string) (authclient.TokenPair, error) {
	if s.refresh == nil {
		panic("unexpected Refresh")
	}
	return s.refresh(ctx, rt)
}
func (s *stubAuthFull) Revoke(ctx context.Context, rt string) error {
	if s.revoke == nil {
		panic("unexpected Revoke")
	}
	return s.revoke(ctx, rt)
}
func (s *stubAuthFull) Providers(ctx context.Context) ([]string, error) {
	if s.providers == nil {
		panic("unexpected Providers")
	}
	return s.providers(ctx)
}

// userStub returns a canned user or error.
type userStub struct {
	user userapi.User
	err  error
}

func (f *userStub) Get(context.Context, string, string) (userapi.User, error) {
	if f.err != nil {
		return userapi.User{}, f.err
	}
	return f.user, nil
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func newRouterFor(t *testing.T, h *Handlers) http.Handler {
	t.Helper()
	return NewRouter(h, nil, testLogger())
}

func TestLoginProviderRedirects(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{
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
}

func TestLoginDevSetsCookieAndGoesHome(t *testing.T) {
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{
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
			h := newTestHandlers(t, newStubCache(), &stubAuthFull{
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
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{
		callback: func(_ context.Context, code, state string) (authclient.TokenPair, error) {
			if code != "c1" || state != "s1" {
				t.Errorf("code=%q state=%q", code, state)
			}
			return authclient.TokenPair{AccessToken: access, RefreshToken: "r1",
				ExpiresIn: 300, RefreshExpiresIn: 2000}, nil
		},
	})
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c1&state=s1", nil))
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
			h := newTestHandlers(t, newStubCache(), &stubAuthFull{
				callback: func(context.Context, string, string) (authclient.TokenPair, error) {
					return authclient.TokenPair{}, tc.err
				},
			})
			rec := httptest.NewRecorder()
			newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c&state=s", nil))
			if rec.Header().Get("Location") != "/login?error="+tc.code {
				t.Errorf("%v: location=%q", tc.err, rec.Header().Get("Location"))
			}
		})
	}
	// Missing params never reach the auth service.
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil))
	if rec.Header().Get("Location") != "/login?error=login_failed" {
		t.Errorf("missing params: location=%q", rec.Header().Get("Location"))
	}
}

func TestLogout(t *testing.T) {
	fc := newStubCache()
	revoked := ""
	h := newTestHandlers(t, fc, &stubAuthFull{
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
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{}) // revoke would panic
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil))
	if rec.Code != http.StatusNoContent || !clearedCookie(rec) {
		t.Fatalf("code=%d cleared=%v", rec.Code, clearedCookie(rec))
	}
}

func TestLogoutSurvivesDependencyOutages(t *testing.T) {
	fc := newStubCache()
	fc.err = errors.New("valkey down")
	h := newTestHandlers(t, fc, &stubAuthFull{
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
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{
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
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{
		providers: func(context.Context) ([]string, error) { return nil, errors.New("auth down") },
	})
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestGetMe(t *testing.T) {
	uid := uuid.New()
	fc := newStubCache()
	avatar := "https://cdn.example/a.png"
	h := newTestHandlers(t, fc, &stubAuthFull{})
	h.users = &userStub{user: userapi.User{
		Id: uid, Email: "alice@example.test", DisplayName: "alice",
		AvatarUrl: &avatar, Roles: []userapi.UserRoles{"user"},
	}}
	access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
	router := newRouterFor(t, h)

	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var me api.Me
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}
	if me.Email != "alice@example.test" || len(me.Roles) != 1 || me.Roles[0] != "user" {
		t.Fatalf("me = %+v", me)
	}
	if fc.me[uid.String()] == nil {
		t.Fatal("composition should be cached")
	}

	// Second call served from cache: break the user client to prove it.
	h.users = &userStub{err: errors.New("must not be called")}
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	router.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached call: code = %d", rec.Code)
	}
}

func TestGetMeUserGone(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.users = &userStub{err: userclient.ErrUserNotFound}
	uid := uuid.New()
	access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized || !clearedCookie(rec) {
		t.Fatalf("vanished account: code=%d cleared=%v", rec.Code, clearedCookie(rec))
	}
}

func TestGetMeUpstreamError(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.users = &userStub{err: errors.New("user service down")}
	uid := uuid.New()
	access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d", rec.Code)
	}
}

// TestGetMeComposesAndCaches proves the
// /api/me composition is cached in real Valkey: the first call composes
// from the real userclient over HTTP and caches the body; the second is
// served from the real Valkey me-cache even after the user service
// starts failing.
func TestGetMeComposesAndCaches(t *testing.T) {
	s := newStack(t)
	const sub = "11111111-1111-1111-1111-111111111111"
	s.users.id, s.users.email, s.users.display = sub, "alice@example.test", "alice"
	access := mintAccess(t, sub, "jA", time.Now().Add(5*time.Minute)) // fresh: no refresh
	cookie := s.cookieFor(t, access, "refresh-1")

	r1 := s.getMe(t, cookie)
	if r1.status != http.StatusOK {
		t.Fatalf("compose call: status = %d body=%s", r1.status, r1.body)
	}
	var me struct {
		Id          string   `json:"id"`
		Email       string   `json:"email"`
		DisplayName string   `json:"display_name"`
		Roles       []string `json:"roles"`
	}
	if err := json.Unmarshal(r1.body, &me); err != nil {
		t.Fatalf("compose body: %v (%s)", err, r1.body)
	}
	if me.Id != sub || me.Email != "alice@example.test" || me.DisplayName != "alice" ||
		len(me.Roles) != 1 || me.Roles[0] != "user" {
		t.Fatalf("composed me = %+v", me)
	}

	// Break the user service: a re-compose would now fail. The second
	// call must be served from the real Valkey me-cache.
	s.users.setMode(userError)
	r2 := s.getMe(t, cookie)
	if r2.status != http.StatusOK {
		t.Fatalf("cached call: status = %d (must be served from real-Valkey me-cache)", r2.status)
	}
	if string(r2.body) != string(r1.body) {
		t.Fatalf("cached body differs:\n first=%s\nsecond=%s", r1.body, r2.body)
	}
}

func TestHealthAndStaticPlaceholder(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	router := newRouterFor(t, h) // nil static handler
	for path, want := range map[string]int{
		"/healthz": http.StatusOK,
		"/readyz":  http.StatusOK,
		"/":        http.StatusNotFound, // static disabled: the dev server owns the frontend
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: code = %d, want %d", path, rec.Code, want)
		}
	}
}
