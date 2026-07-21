package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vg-collect/services/bff/internal/authclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/enrichmentclient"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/authapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/collectionapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/enrichapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vg-collect/services/bff/internal/session"
	"github.com/levonn-dev/vg-collect/services/bff/internal/userclient"
)

// stubAuthFull lets each test override exactly the methods it expects.
type stubAuthFull struct {
	start          func(ctx context.Context, provider string) (string, error)
	callback       func(ctx context.Context, code, state string) (authclient.TokenPair, error)
	dev            func(ctx context.Context, user string) (authclient.TokenPair, error)
	refresh        func(ctx context.Context, rt string) (authclient.TokenPair, error)
	revoke         func(ctx context.Context, rt string) error
	providers      func(ctx context.Context) ([]string, error)
	linkStart      func(ctx context.Context, provider, bearer string) (string, error)
	devLink        func(ctx context.Context, user, bearer string) (authclient.TokenPair, error)
	listIdentities func(ctx context.Context, userID, bearer string) ([]authapi.Identity, error)
	deleteIdentity func(ctx context.Context, identityID uuid.UUID, bearer string) error
	deleteUserAuth func(ctx context.Context, userID, bearer string) error
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
func (s *stubAuthFull) LinkStart(ctx context.Context, provider, bearer string) (string, error) {
	if s.linkStart == nil {
		panic("unexpected LinkStart")
	}
	return s.linkStart(ctx, provider, bearer)
}
func (s *stubAuthFull) DevLink(ctx context.Context, user, bearer string) (authclient.TokenPair, error) {
	if s.devLink == nil {
		panic("unexpected DevLink")
	}
	return s.devLink(ctx, user, bearer)
}
func (s *stubAuthFull) ListIdentities(ctx context.Context, userID, bearer string) ([]authapi.Identity, error) {
	if s.listIdentities == nil {
		panic("unexpected ListIdentities")
	}
	return s.listIdentities(ctx, userID, bearer)
}
func (s *stubAuthFull) DeleteIdentity(ctx context.Context, identityID uuid.UUID, bearer string) error {
	if s.deleteIdentity == nil {
		panic("unexpected DeleteIdentity")
	}
	return s.deleteIdentity(ctx, identityID, bearer)
}
func (s *stubAuthFull) DeleteUserAuth(ctx context.Context, userID, bearer string) error {
	if s.deleteUserAuth == nil {
		panic("unexpected DeleteUserAuth")
	}
	return s.deleteUserAuth(ctx, userID, bearer)
}

// stubUsersFull returns a canned user/result or error. onDelete, when set, is
// notified on every call so a test can record DeleteMe's cross-service
// call order.
type stubUsersFull struct {
	user     userapi.User
	err      error
	result   userclient.Result
	onDelete func()
}

func (f *stubUsersFull) Get(context.Context, string, string) (userapi.User, error) {
	if f.err != nil {
		return userapi.User{}, f.err
	}
	return f.user, nil
}

func (f *stubUsersFull) Update(context.Context, string, string, []byte) (userclient.Result, error) {
	if f.err != nil {
		return userclient.Result{}, f.err
	}
	return f.result, nil
}

func (f *stubUsersFull) Delete(_ context.Context, _, _ string) error {
	if f.onDelete != nil {
		f.onDelete()
	}
	return f.err
}

// stubEnrichment implements server.EnrichmentAPI via function fields.
type stubEnrichment struct {
	search  func(ctx context.Context, bearer, typ, q string) (enrichmentclient.Result, error)
	resolve func(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error)
	product func(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error)
	score   func(ctx context.Context, bearer string, req enrichapi.ScoreRequest) ([]byte, bool, error)
	fx      func(ctx context.Context, bearer string) (enrichmentclient.Result, error)

	listPlatforms func(ctx context.Context, bearer string) (enrichmentclient.Result, error)

	unmatchedProducts func(ctx context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error)
	communityProducts func(ctx context.Context, bearer string, params *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error)
	setProductMapping func(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)
	triggerRefresh    func(ctx context.Context, bearer string) (enrichmentclient.Result, error)
	deleteProduct     func(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error)

	createCommunityProduct  func(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error)
	promoteProduct          func(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)
	promoteCandidates       func(ctx context.Context, bearer string, params *enrichapi.ListPromoteCandidatesParams) (enrichmentclient.Result, error)
	dismissPromoteCandidate func(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error)
}

var _ EnrichmentAPI = (*stubEnrichment)(nil)

func (s *stubEnrichment) Search(ctx context.Context, bearer, typ, q string) (enrichmentclient.Result, error) {
	if s.search == nil {
		panic("unexpected Search")
	}
	return s.search(ctx, bearer, typ, q)
}

func (s *stubEnrichment) Resolve(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error) {
	if s.resolve == nil {
		panic("unexpected Resolve")
	}
	return s.resolve(ctx, bearer, body)
}

func (s *stubEnrichment) Product(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error) {
	if s.product == nil {
		panic("unexpected Product")
	}
	return s.product(ctx, bearer, id)
}

func (s *stubEnrichment) Score(ctx context.Context, bearer string, req enrichapi.ScoreRequest) ([]byte, bool, error) {
	if s.score == nil {
		panic("unexpected Score")
	}
	return s.score(ctx, bearer, req)
}

func (s *stubEnrichment) FX(ctx context.Context, bearer string) (enrichmentclient.Result, error) {
	if s.fx == nil {
		panic("unexpected FX")
	}
	return s.fx(ctx, bearer)
}

func (s *stubEnrichment) ListPlatforms(ctx context.Context, bearer string) (enrichmentclient.Result, error) {
	if s.listPlatforms == nil {
		panic("unexpected ListPlatforms")
	}
	return s.listPlatforms(ctx, bearer)
}

func (s *stubEnrichment) UnmatchedProducts(ctx context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error) {
	if s.unmatchedProducts == nil {
		panic("unexpected UnmatchedProducts")
	}
	return s.unmatchedProducts(ctx, bearer, params)
}

func (s *stubEnrichment) CommunityProducts(ctx context.Context, bearer string, params *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error) {
	if s.communityProducts == nil {
		panic("unexpected CommunityProducts")
	}
	return s.communityProducts(ctx, bearer, params)
}

func (s *stubEnrichment) SetProductMapping(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error) {
	if s.setProductMapping == nil {
		panic("unexpected SetProductMapping")
	}
	return s.setProductMapping(ctx, bearer, id, body)
}

func (s *stubEnrichment) TriggerRefresh(ctx context.Context, bearer string) (enrichmentclient.Result, error) {
	if s.triggerRefresh == nil {
		panic("unexpected TriggerRefresh")
	}
	return s.triggerRefresh(ctx, bearer)
}

func (s *stubEnrichment) DeleteProduct(ctx context.Context, bearer string, id uuid.UUID) (enrichmentclient.Result, error) {
	if s.deleteProduct == nil {
		panic("unexpected DeleteProduct")
	}
	return s.deleteProduct(ctx, bearer, id)
}

func (s *stubEnrichment) CreateCommunityProduct(ctx context.Context, bearer string, body []byte) (enrichmentclient.Result, error) {
	if s.createCommunityProduct == nil {
		panic("unexpected CreateCommunityProduct")
	}
	return s.createCommunityProduct(ctx, bearer, body)
}

func (s *stubEnrichment) PromoteProduct(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error) {
	if s.promoteProduct == nil {
		panic("unexpected PromoteProduct")
	}
	return s.promoteProduct(ctx, bearer, id, body)
}

func (s *stubEnrichment) PromoteCandidates(ctx context.Context, bearer string, params *enrichapi.ListPromoteCandidatesParams) (enrichmentclient.Result, error) {
	if s.promoteCandidates == nil {
		panic("unexpected PromoteCandidates")
	}
	return s.promoteCandidates(ctx, bearer, params)
}

func (s *stubEnrichment) DismissPromoteCandidate(ctx context.Context, bearer string, id uuid.UUID, body []byte) (enrichmentclient.Result, error) {
	if s.dismissPromoteCandidate == nil {
		panic("unexpected DismissPromoteCandidate")
	}
	return s.dismissPromoteCandidate(ctx, bearer, id, body)
}

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func newRouterFor(t *testing.T, h *Handlers) http.Handler {
	t.Helper()
	return NewRouter(h, nil, testLogger())
}

// testEnv bundles the session cookie and the raw access token it seals,
// so a pass-through test can both drive an authenticated request and
// assert the exact bearer that must ride the proxied call.
type testEnv struct {
	cookie             *http.Cookie
	sessionAccessToken string
}

// newTestHandlersWithEnrichment builds Handlers wired to enrich with a
// fresh (never-refreshing) session ready to drive the pass-through
// routes.
func newTestHandlersWithEnrichment(t *testing.T, enrich *stubEnrichment) (*Handlers, *testEnv) {
	t.Helper()
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.enrichment = enrich
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
}

// doAuthed drives method/path through h's router carrying env's sealed
// session cookie.
func doAuthed(t *testing.T, h *Handlers, env *testEnv, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.AddCookie(env.cookie)
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	return rec
}

// doUnauthed drives method/path through h's router with no session
// cookie at all: the guard must answer before any handler runs.
func doUnauthed(t *testing.T, h *Handlers, env *testEnv, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	_ = env
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// doAuthedBody mirrors doAuthed for a mutating request: env's sealed
// session cookie, an allowed Origin (CheckOrigin runs ahead of the
// handler), and body as the JSON request body.
func doAuthedBody(t *testing.T, h *Handlers, env *testEnv, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.AddCookie(env.cookie)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	return rec
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

func TestCallbackLinkOutcomes(t *testing.T) {
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	google := "google"

	t.Run("link_success_redirects_to_account_with_provider", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{AccessToken: access, RefreshToken: "r1",
					ExpiresIn: 300, RefreshExpiresIn: 2000, LinkedProvider: &google}, nil
			},
		})
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c1&state=s1", nil))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?linked=google" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		cs := rec.Result().Cookies()
		if len(cs) != 1 || cs[0].Name != session.CookieName {
			t.Fatalf("cookies = %+v", cs)
		}
		if opened, err := h.codec.Open(cs[0].Value); err != nil || opened.RefreshToken != "r1" {
			t.Fatalf("cookie content: %+v err=%v", opened, err)
		}
	})

	t.Run("conflict_redirects_without_a_cookie", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{}, authclient.ErrLinkConflict
			},
		})
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c&state=s", nil))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?link_error=conflict" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("conflict must not set a cookie: %+v", rec.Result().Cookies())
		}
	})

	t.Run("email_unverified_redirects_with_link_error", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{}, authclient.ErrLinkEmailUnverified
			},
		})
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c&state=s", nil))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?link_error=email_unverified" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("unverified must not set a cookie: %+v", rec.Result().Cookies())
		}
	})

	t.Run("plain_login_still_redirects_home", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			callback: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{AccessToken: access, RefreshToken: "r1",
					ExpiresIn: 300, RefreshExpiresIn: 2000}, nil
			},
		})
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=c&state=s", nil))
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
	})
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
	h.users = &stubUsersFull{user: userapi.User{
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
	h.users = &stubUsersFull{err: errors.New("must not be called")}
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
	h.users = &stubUsersFull{err: userclient.ErrUserNotFound}
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
	h.users = &stubUsersFull{err: errors.New("user service down")}
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

func TestUpdateMe_RelaysAndInvalidatesCache(t *testing.T) {
	uid := uuid.New()

	t.Run("200_relays_projection_and_invalidates_cache", func(t *testing.T) {
		userJSON := []byte(`{"id":"` + uid.String() + `","email":"alice@example.test","display_name":"alice2","roles":["user"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`)
		fc := newStubCache()
		fc.me[uid.String()] = []byte(`{"stale":true}`)
		h := newTestHandlers(t, fc, &stubAuthFull{})
		h.users = &stubUsersFull{result: userclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: userJSON}}
		access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodPatch, "/api/me", strings.NewReader(`{"display_name":"alice2"}`))
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
		}
		var raw map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		if _, has := raw["created_at"]; has {
			t.Fatalf("the Me projection must not carry timestamps: %v", raw)
		}
		var me api.Me
		if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
			t.Fatal(err)
		}
		if me.Id != uid || me.Email != "alice@example.test" || me.DisplayName != "alice2" ||
			len(me.Roles) != 1 || me.Roles[0] != "user" {
			t.Fatalf("me = %+v", me)
		}
		if fc.me[uid.String()] != nil {
			t.Fatal("a successful update must invalidate the /api/me cache")
		}
	})

	t.Run("400_relays_verbatim_and_does_not_invalidate", func(t *testing.T) {
		problemJSON := []byte(`{"type":"about:blank","title":"Bad Request","status":400,"code":"invalid_body","detail":"display_name too long"}`)
		fc := newStubCache()
		fc.me[uid.String()] = []byte(`{"cached":true}`)
		h := newTestHandlers(t, fc, &stubAuthFull{})
		h.users = &stubUsersFull{result: userclient.Result{Status: http.StatusBadRequest, ContentType: "application/problem+json", Body: problemJSON}}
		access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodPatch, "/api/me", strings.NewReader(`{"display_name":""}`))
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Type") != "application/problem+json" ||
			rec.Body.String() != string(problemJSON) {
			t.Fatalf("relay: code=%d ct=%q body=%s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
		}
		if fc.me[uid.String()] == nil {
			t.Fatal("a rejected update must not invalidate the /api/me cache")
		}
	})

	t.Run("upstream_error_is_502", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
		h.users = &stubUsersFull{err: errors.New("user service down")}
		access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodPatch, "/api/me", strings.NewReader(`{"display_name":"x"}`))
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("code = %d", rec.Code)
		}
	})
}

// TestUnitGetMe_IncludesPreferredCurrency pins that the profile's
// currency reaches the browser projection.
func TestUnitGetMe_IncludesPreferredCurrency(t *testing.T) {
	uid := uuid.New()
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.users = &stubUsersFull{user: userapi.User{
		Id: uid, Email: "alice@example.test", DisplayName: "alice",
		Roles: []userapi.UserRoles{"user"}, PreferredCurrency: "EUR",
	}}
	access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var got struct {
		PreferredCurrency string `json:"preferred_currency"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PreferredCurrency != "EUR" {
		t.Fatalf("preferred_currency: %q, want EUR", got.PreferredCurrency)
	}
}

// captureUsers embeds the stub so Get and Delete forward unchanged,
// while Update additionally exposes the raw body reaching the user
// service (mirrors captureCollection's pass-through capture).
type captureUsers struct {
	*stubUsersFull
	onUpdate func(body []byte)
}

func (c *captureUsers) Update(ctx context.Context, id, bearer string, body []byte) (userclient.Result, error) {
	c.onUpdate(body)
	return c.stubUsersFull.Update(ctx, id, bearer, body)
}

// TestUnitUpdateMe_RelaysPreferredCurrency pins that the PATCH body
// reaches the user service verbatim and the answer projects the field.
func TestUnitUpdateMe_RelaysPreferredCurrency(t *testing.T) {
	uid := uuid.New()
	userJSON := []byte(`{"id":"` + uid.String() + `","email":"alice@example.test","display_name":"alice","preferred_currency":"JPY","roles":["user"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`)
	users := &stubUsersFull{result: userclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: userJSON}}
	var gotBody []byte
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.users = &captureUsers{stubUsersFull: users, onUpdate: func(body []byte) { gotBody = body }}
	access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
	r := httptest.NewRequest(http.MethodPatch, "/api/me", strings.NewReader(`{"preferred_currency":"JPY"}`))
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)

	if !strings.Contains(string(gotBody), `"preferred_currency":"JPY"`) {
		t.Fatalf("relayed body = %s, want it to contain preferred_currency JPY", gotBody)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		PreferredCurrency string `json:"preferred_currency"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.PreferredCurrency != "JPY" {
		t.Fatalf("preferred_currency: %q, want JPY", got.PreferredCurrency)
	}
}

func TestGetMyIdentities(t *testing.T) {
	t.Run("200_in_list_order", func(t *testing.T) {
		uid := uuid.New()
		id1, id2 := uuid.New(), uuid.New()
		emailA := "alice@example.test"
		t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			listIdentities: func(_ context.Context, userID, _ string) ([]authapi.Identity, error) {
				if userID != uid.String() {
					t.Errorf("userID = %q", userID)
				}
				return []authapi.Identity{
					{Id: id1, Provider: "google", Email: &emailA, CreatedAt: t1},
					{Id: id2, Provider: "dev", CreatedAt: t2},
				}, nil
			},
		})
		access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodGet, "/api/me/identities", nil)
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
		}
		var body api.Identities
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Identities) != 2 ||
			body.Identities[0].Id != id1 || body.Identities[0].Provider != "google" ||
			body.Identities[0].Email == nil || *body.Identities[0].Email != emailA ||
			body.Identities[1].Id != id2 || body.Identities[1].Provider != "dev" || body.Identities[1].Email != nil {
			t.Fatalf("identities = %+v", body.Identities)
		}
	})

	t.Run("upstream_error_is_502", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			listIdentities: func(context.Context, string, string) ([]authapi.Identity, error) {
				return nil, errors.New("auth down")
			},
		})
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodGet, "/api/me/identities", nil)
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusBadGateway {
			t.Fatalf("code = %d", rec.Code)
		}
	})
}

func TestDeleteMyIdentity(t *testing.T) {
	iid := uuid.New()
	cases := []struct {
		name        string
		err         error
		code        int
		problemCode string
	}{
		{"unlinked", nil, http.StatusNoContent, ""},
		{"last_identity", authclient.ErrLastIdentity, http.StatusConflict, "last_identity"},
		{"not_found", authclient.ErrIdentityNotFound, http.StatusNotFound, "identity_not_found"},
		{"upstream_error", errors.New("boom"), http.StatusBadGateway, "upstream_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandlers(t, newStubCache(), &stubAuthFull{
				deleteIdentity: func(_ context.Context, gotID uuid.UUID, _ string) error {
					if gotID != iid {
						t.Errorf("identityID = %v", gotID)
					}
					return tc.err
				},
			})
			access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
			r := httptest.NewRequest(http.MethodDelete, "/api/me/identities/"+iid.String(), nil)
			r.AddCookie(sealedCookie(t, h, access, "r1"))
			rec := httptest.NewRecorder()
			newRouterFor(t, h).ServeHTTP(rec, r)
			if rec.Code != tc.code {
				t.Fatalf("code = %d, want %d body=%s", rec.Code, tc.code, rec.Body.String())
			}
			if tc.problemCode != "" {
				var p struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != tc.problemCode {
					t.Fatalf("problem code = %+v err=%v", p, err)
				}
			}
		})
	}
}

func TestLinkLoginNavigations(t *testing.T) {
	t.Run("dev_links_and_sets_a_fresh_cookie", func(t *testing.T) {
		linkedAccess := mintAccess(t, "u1", "jlinked", time.Now().Add(5*time.Minute))
		linked := "dev"
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			devLink: func(_ context.Context, user, _ string) (authclient.TokenPair, error) {
				if user != "bob" {
					t.Errorf("user = %q", user)
				}
				return authclient.TokenPair{AccessToken: linkedAccess, RefreshToken: "r2",
					ExpiresIn: 300, RefreshExpiresIn: 2000, LinkedProvider: &linked}, nil
			},
		})
		sessAccess := mintAccess(t, "u1", "jsess", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodGet, "/api/auth/link?provider=dev&user=bob", nil)
		r.AddCookie(sealedCookie(t, h, sessAccess, "rsess"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?linked=dev" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		cs := rec.Result().Cookies()
		if len(cs) != 1 || cs[0].Name != session.CookieName {
			t.Fatalf("cookies = %+v", cs)
		}
		if opened, err := h.codec.Open(cs[0].Value); err != nil || opened.AccessToken != linkedAccess || opened.RefreshToken != "r2" {
			t.Fatalf("cookie should seal the freshly-linked pair: %+v err=%v", opened, err)
		}
	})

	t.Run("dev_conflict_redirects_without_a_cookie", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			devLink: func(context.Context, string, string) (authclient.TokenPair, error) {
				return authclient.TokenPair{}, authclient.ErrLinkConflict
			},
		})
		sessAccess := mintAccess(t, "u1", "jsess", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodGet, "/api/auth/link?provider=dev&user=bob", nil)
		r.AddCookie(sealedCookie(t, h, sessAccess, "rsess"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?link_error=conflict" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
		if len(rec.Result().Cookies()) != 0 {
			t.Fatalf("conflict must not set a cookie: %+v", rec.Result().Cookies())
		}
	})

	t.Run("google_redirects_to_the_authorize_url", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			linkStart: func(_ context.Context, provider, _ string) (string, error) {
				if provider != "google" {
					t.Errorf("provider = %q", provider)
				}
				return "https://idp.example/authorize?state=link1", nil
			},
		})
		sessAccess := mintAccess(t, "u1", "jsess", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodGet, "/api/auth/link?provider=google", nil)
		r.AddCookie(sealedCookie(t, h, sessAccess, "rsess"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://idp.example/authorize?state=link1" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("google_start_failure_redirects_link_failed", func(t *testing.T) {
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			linkStart: func(context.Context, string, string) (string, error) {
				return "", errors.New("boom")
			},
		})
		sessAccess := mintAccess(t, "u1", "jsess", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodGet, "/api/auth/link?provider=google", nil)
		r.AddCookie(sealedCookie(t, h, sessAccess, "rsess"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/account?link_error=link_failed" {
			t.Fatalf("code=%d location=%q", rec.Code, rec.Header().Get("Location"))
		}
	})
}

func TestDeleteMe_OrchestrationOrderAndFailure(t *testing.T) {
	t.Run("happy_path_orchestrates_purge_then_auth_then_user_then_session", func(t *testing.T) {
		var mu sync.Mutex
		var order []string
		record := func(step string) {
			mu.Lock()
			order = append(order, step)
			mu.Unlock()
		}
		uid := uuid.New()
		fc := newStubCache()
		fc.me[uid.String()] = []byte(`{"cached":true}`)
		fc.recs[uid.String()] = []byte(`{"cached":true}`)
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			record("collection.PurgeUserData")
			return collectionclient.Result{Status: http.StatusNoContent}, nil
		}}
		h := newTestHandlers(t, fc, &stubAuthFull{
			deleteUserAuth: func(context.Context, string, string) error {
				record("auth.DeleteUserAuth")
				return nil
			},
		})
		h.collection = col
		h.users = &stubUsersFull{onDelete: func() { record("users.Delete") }}
		access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodDelete, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
		}
		if want := []string{"collection.PurgeUserData", "auth.DeleteUserAuth", "users.Delete"}; !slices.Equal(order, want) {
			t.Fatalf("order = %v, want %v", order, want)
		}
		if len(fc.denyAdds) != 1 || fc.denyAdds[0][0] != "j1" {
			t.Fatalf("denylist adds = %v", fc.denyAdds)
		}
		if fc.me[uid.String()] != nil {
			t.Fatal("deletion must invalidate the /api/me cache")
		}
		if fc.recs[uid.String()] != nil {
			t.Fatal("deletion must invalidate the recommendations cache")
		}
		if !clearedCookie(rec) {
			t.Fatal("deletion must clear the session cookie")
		}
	})

	t.Run("purge_failure_stops_before_auth_or_user", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			res  collectionclient.Result
			err  error
		}{
			{"non_204_result", collectionclient.Result{Status: http.StatusInternalServerError}, nil},
			{"transport_error", collectionclient.Result{}, errors.New("collection down")},
		} {
			t.Run(tc.name, func(t *testing.T) {
				var mu sync.Mutex
				var order []string
				record := func(step string) {
					mu.Lock()
					order = append(order, step)
					mu.Unlock()
				}
				col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
					record("collection.PurgeUserData")
					return tc.res, tc.err
				}}
				h := newTestHandlers(t, newStubCache(), &stubAuthFull{
					deleteUserAuth: func(context.Context, string, string) error {
						record("auth.DeleteUserAuth")
						return nil
					},
				})
				h.collection = col
				h.users = &stubUsersFull{onDelete: func() { record("users.Delete") }}
				access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
				r := httptest.NewRequest(http.MethodDelete, "/api/me", nil)
				r.AddCookie(sealedCookie(t, h, access, "r1"))
				rec := httptest.NewRecorder()
				newRouterFor(t, h).ServeHTTP(rec, r)

				if rec.Code != http.StatusBadGateway {
					t.Fatalf("code = %d", rec.Code)
				}
				if want := []string{"collection.PurgeUserData"}; !slices.Equal(order, want) {
					t.Fatalf("order = %v, want %v (auth/user must not run)", order, want)
				}
				if clearedCookie(rec) {
					t.Fatal("a mid-failure must keep the session intact")
				}
			})
		}
	})

	t.Run("auth_failure_stops_before_user", func(t *testing.T) {
		var mu sync.Mutex
		var order []string
		record := func(step string) {
			mu.Lock()
			order = append(order, step)
			mu.Unlock()
		}
		col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
			record("collection.PurgeUserData")
			return collectionclient.Result{Status: http.StatusNoContent}, nil
		}}
		h := newTestHandlers(t, newStubCache(), &stubAuthFull{
			deleteUserAuth: func(context.Context, string, string) error {
				record("auth.DeleteUserAuth")
				return errors.New("auth down")
			},
		})
		h.collection = col
		h.users = &stubUsersFull{onDelete: func() { record("users.Delete") }}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodDelete, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)

		if rec.Code != http.StatusBadGateway {
			t.Fatalf("code = %d", rec.Code)
		}
		if want := []string{"collection.PurgeUserData", "auth.DeleteUserAuth"}; !slices.Equal(order, want) {
			t.Fatalf("order = %v, want %v (user delete must not run)", order, want)
		}
		if clearedCookie(rec) {
			t.Fatal("a mid-failure must keep the session intact")
		}
	})
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

func TestUnitSearchPassThrough_RelaysAndForwardsBearer(t *testing.T) {
	var gotBearer string
	enrich := &stubEnrichment{search: func(_ context.Context, bearer, typ, q string) (enrichmentclient.Result, error) {
		gotBearer = bearer
		if typ != "game" || q != "zelda" {
			t.Fatalf("params: %s %s", typ, q)
		}
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{"degraded":false,"results":[]}`)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/search?type=game&q=zelda")
	if rec.Code != 200 || rec.Body.String() != `{"degraded":false,"results":[]}` {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer == "" || gotBearer != env.sessionAccessToken {
		t.Fatalf("the session's access token must ride the proxied call, got %q", gotBearer)
	}
}

func TestUnitSearchPassThrough_UpstreamFailureIs502(t *testing.T) {
	enrich := &stubEnrichment{search: func(context.Context, string, string, string) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/search?type=game&q=zelda")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream failure: %d", rec.Code)
	}
}

// TestUnitFxRelay_SnapshotRelaysVerbatim pins that /api/fx passes the
// enrichment answer through byte-for-byte with the user's own token.
func TestUnitFxRelay_SnapshotRelaysVerbatim(t *testing.T) {
	const relayed = `{"base":"USD","date":"2026-07-01","rates":{"EUR":0.5,"JPY":150}}`
	var gotBearer string
	enrich := &stubEnrichment{fx: func(_ context.Context, bearer string) (enrichmentclient.Result, error) {
		gotBearer = bearer
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(relayed)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/fx")
	if rec.Code != 200 || rec.Body.String() != relayed {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer reaching enrichment: %q", gotBearer)
	}
}

// TestUnitFxRelay_UpstreamProblemRelaysVerbatim pins that a 502
// problem from enrichment (cold fx cache) reaches the browser with
// status, content type, and body intact.
func TestUnitFxRelay_UpstreamProblemRelaysVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Bad Gateway","status":502,"code":"upstream_unavailable","detail":"exchange rates are unavailable"}`
	enrich := &stubEnrichment{fx: func(context.Context, string) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 502, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/fx")
	if rec.Code != 502 || rec.Body.String() != problem {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type: %q", ct)
	}
}

func TestUnitFxRelay_ClientErrorAnswers502(t *testing.T) {
	enrich := &stubEnrichment{fx: func(context.Context, string) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/fx")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

// TestUnitSearchPassThrough_PcListingTypeRelaysVerbatim pins that
// type=pc_listing is not blocked by any bff-side enum check: the
// generated query binding treats type as an opaque string (enrichment
// owns validation), so it must reach the stub exactly as sent and the
// stub's body must round-trip to the client untouched.
func TestUnitSearchPassThrough_PcListingTypeRelaysVerbatim(t *testing.T) {
	const relayed = `{"degraded":false,"results":[{"type":"pc_listing","pc_product_id":5099,"name":"Super Mario Bros. (NES)"}]}`
	var gotType, gotQ string
	enrich := &stubEnrichment{search: func(_ context.Context, _, typ, q string) (enrichmentclient.Result, error) {
		gotType, gotQ = typ, q
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(relayed)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/search?type=pc_listing&q=mario")
	if gotType != "pc_listing" || gotQ != "mario" {
		t.Fatalf("params reaching enrichment: type=%q q=%q, want pc_listing/mario", gotType, gotQ)
	}
	if rec.Code != 200 || rec.Body.String() != relayed {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitResolvePassThrough_PcListingBodyRelaysVerbatim pins that a
// pc_listing resolve body reaches the enrichment stub byte-for-byte -
// the bff reads and forwards raw bytes, it never decodes into
// ResolveRequest - and the stub's answer round-trips to the client.
func TestUnitResolvePassThrough_PcListingBodyRelaysVerbatim(t *testing.T) {
	const sent = `{"type":"pc_listing","pc_product_id":5099}`
	const relayed = `{"id":"22222222-2222-2222-2222-222222222222","type":"pc_listing","pc_product_id":5099}`
	var gotBody []byte
	enrich := &stubEnrichment{resolve: func(_ context.Context, _ string, body []byte) (enrichmentclient.Result, error) {
		gotBody = body
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(relayed)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	req := httptest.NewRequest(http.MethodPost, "/api/products/resolve", strings.NewReader(sent))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if string(gotBody) != sent {
		t.Fatalf("body reaching enrichment: %s, want unmodified %s", gotBody, sent)
	}
	if rec.Code != 200 || rec.Body.String() != relayed {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnitProductPassThrough_RelaysProblemBody(t *testing.T) {
	enrich := &stubEnrichment{product: func(context.Context, string, uuid.UUID) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 404, ContentType: "application/problem+json",
			Body: []byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"product_not_found"}`)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/products/11111111-1111-1111-1111-111111111111")
	if rec.Code != 404 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("problem relay: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestUnitPassThroughs_RequireSession(t *testing.T) {
	h, env := newTestHandlersWithEnrichment(t, &stubEnrichment{})
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/search?type=game&q=zelda"},
		{http.MethodPost, "/api/products/resolve"},
		{http.MethodGet, "/api/products/11111111-1111-1111-1111-111111111111"},
	} {
		rec := doUnauthed(t, h, env, tc.method, tc.path)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without a session: %d", tc.method, tc.path, rec.Code)
		}
	}
}

// stubCollection implements server.CollectionAPI via function fields;
// the route-matrix test only needs the generic answer field.
type stubCollection struct {
	answer  func(op string) (collectionclient.Result, error)
	library func(ctx context.Context, bearer string) (collectionapi.LibrarySummary, error)

	createSubmission func(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	getSubmission    func(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	cancelSubmission func(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	ackSubmission    func(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error)
	listSubmissions  func(ctx context.Context, bearer string, params *collectionapi.ListSubmissionsParams) (collectionclient.Result, error)
	submitVerdict    func(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error)

	mu        sync.Mutex
	gotBearer []string
	gotOps    []string
}

func (s *stubCollection) call(op, bearer string) (collectionclient.Result, error) {
	s.mu.Lock()
	s.gotOps = append(s.gotOps, op)
	s.gotBearer = append(s.gotBearer, bearer)
	s.mu.Unlock()
	if s.answer == nil {
		panic("unexpected collection call: " + op)
	}
	return s.answer(op)
}

func (s *stubCollection) ListEntries(_ context.Context, bearer string, _ *collectionapi.ListEntriesParams) (collectionclient.Result, error) {
	return s.call("list_entries", bearer)
}
func (s *stubCollection) CreateEntry(_ context.Context, bearer string, _ []byte) (collectionclient.Result, error) {
	return s.call("create_entry", bearer)
}
func (s *stubCollection) GetEntry(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("get_entry", bearer)
}
func (s *stubCollection) UpdateEntry(_ context.Context, bearer string, _ uuid.UUID, _ []byte) (collectionclient.Result, error) {
	return s.call("update_entry", bearer)
}
func (s *stubCollection) DeleteEntry(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("delete_entry", bearer)
}
func (s *stubCollection) ReorderEntry(_ context.Context, bearer string, _ uuid.UUID, _ []byte) (collectionclient.Result, error) {
	return s.call("reorder_entry", bearer)
}
func (s *stubCollection) ListTags(_ context.Context, bearer string) (collectionclient.Result, error) {
	return s.call("list_tags", bearer)
}
func (s *stubCollection) CreateTag(_ context.Context, bearer string, _ []byte) (collectionclient.Result, error) {
	return s.call("create_tag", bearer)
}
func (s *stubCollection) RenameTag(_ context.Context, bearer string, _ uuid.UUID, _ []byte) (collectionclient.Result, error) {
	return s.call("rename_tag", bearer)
}
func (s *stubCollection) DeleteTag(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("delete_tag", bearer)
}
func (s *stubCollection) ListViews(_ context.Context, bearer string) (collectionclient.Result, error) {
	return s.call("list_views", bearer)
}
func (s *stubCollection) CreateView(_ context.Context, bearer string, _ []byte) (collectionclient.Result, error) {
	return s.call("create_view", bearer)
}
func (s *stubCollection) UpdateView(_ context.Context, bearer string, _ uuid.UUID, _ []byte) (collectionclient.Result, error) {
	return s.call("update_view", bearer)
}
func (s *stubCollection) DeleteView(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("delete_view", bearer)
}
func (s *stubCollection) GetDashboard(_ context.Context, bearer string, _ *collectionapi.GetDashboardParams) (collectionclient.Result, error) {
	return s.call("dashboard", bearer)
}
func (s *stubCollection) GetValueHistory(_ context.Context, bearer string) (collectionclient.Result, error) {
	return s.call("value_history", bearer)
}
func (s *stubCollection) LibrarySummary(ctx context.Context, bearer string) (collectionapi.LibrarySummary, error) {
	if s.library == nil {
		panic("unexpected LibrarySummary")
	}
	return s.library(ctx, bearer)
}
func (s *stubCollection) PurgeUserData(_ context.Context, bearer string) (collectionclient.Result, error) {
	return s.call("purge_user_data", bearer)
}

func (s *stubCollection) CountProductReferences(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
	return s.call("count_product_references", bearer)
}

func (s *stubCollection) CreateSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error) {
	if s.createSubmission == nil {
		panic("unexpected CreateSubmission")
	}
	return s.createSubmission(ctx, bearer, id)
}

func (s *stubCollection) GetSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error) {
	if s.getSubmission == nil {
		panic("unexpected GetSubmission")
	}
	return s.getSubmission(ctx, bearer, id)
}

func (s *stubCollection) CancelSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error) {
	if s.cancelSubmission == nil {
		panic("unexpected CancelSubmission")
	}
	return s.cancelSubmission(ctx, bearer, id)
}

func (s *stubCollection) AckSubmission(ctx context.Context, bearer string, id uuid.UUID) (collectionclient.Result, error) {
	if s.ackSubmission == nil {
		panic("unexpected AckSubmission")
	}
	return s.ackSubmission(ctx, bearer, id)
}

func (s *stubCollection) ListSubmissions(ctx context.Context, bearer string, params *collectionapi.ListSubmissionsParams) (collectionclient.Result, error) {
	if s.listSubmissions == nil {
		panic("unexpected ListSubmissions")
	}
	return s.listSubmissions(ctx, bearer, params)
}

func (s *stubCollection) SubmitVerdict(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error) {
	if s.submitVerdict == nil {
		panic("unexpected SubmitVerdict")
	}
	return s.submitVerdict(ctx, bearer, id, body)
}

var _ CollectionAPI = (*stubCollection)(nil)

// newTestHandlersWithCollection wires a session-ready Handlers around
// the collection stub.
func newTestHandlersWithCollection(t *testing.T, col *stubCollection) (*Handlers, *testEnv) {
	t.Helper()
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.collection = col
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
}

func TestUnitCollectionPassThroughs_RouteMatrix(t *testing.T) {
	id := uuid.NewString()
	routes := []struct {
		method, path, op string
		body             string
		status           int
	}{
		{http.MethodGet, "/api/entries?status=backlog", "list_entries", "", 200},
		{http.MethodPost, "/api/entries", "create_entry", `{"product_id":"x"}`, 201},
		{http.MethodGet, "/api/entries/" + id, "get_entry", "", 200},
		{http.MethodPut, "/api/entries/" + id, "update_entry", `{}`, 200},
		{http.MethodDelete, "/api/entries/" + id, "delete_entry", "", 204},
		{http.MethodPost, "/api/entries/" + id + "/reorder", "reorder_entry", `{"after_id":null}`, 200},
		{http.MethodGet, "/api/tags", "list_tags", "", 200},
		{http.MethodPost, "/api/tags", "create_tag", `{"name":"x"}`, 201},
		{http.MethodPut, "/api/tags/" + id, "rename_tag", `{"name":"y"}`, 200},
		{http.MethodDelete, "/api/tags/" + id, "delete_tag", "", 204},
		{http.MethodGet, "/api/views", "list_views", "", 200},
		{http.MethodPost, "/api/views", "create_view", `{"name":"v","params":{}}`, 201},
		{http.MethodPut, "/api/views/" + id, "update_view", `{"name":"v","params":{}}`, 200},
		{http.MethodDelete, "/api/views/" + id, "delete_view", "", 204},
		{http.MethodGet, "/api/dashboard", "dashboard", "", 200},
	}
	for _, rt := range routes {
		t.Run(rt.op, func(t *testing.T) {
			col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
				if op != rt.op {
					t.Fatalf("routed to %q, want %q", op, rt.op)
				}
				return collectionclient.Result{Status: rt.status, ContentType: "application/json", Body: []byte(`{"ok":true}`)}, nil
			}}
			h, env := newTestHandlersWithCollection(t, col)
			var body io.Reader
			if rt.body != "" {
				body = strings.NewReader(rt.body)
			}
			req := httptest.NewRequest(rt.method, rt.path, body)
			req.AddCookie(env.cookie)
			if rt.body != "" {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Origin", "http://localhost:8090")
			}
			rec := httptest.NewRecorder()
			newRouterFor(t, h).ServeHTTP(rec, req)
			if rec.Code != rt.status {
				t.Fatalf("relay status: %d, want %d (%s)", rec.Code, rt.status, rec.Body.String())
			}
			if got := col.gotBearer[len(col.gotBearer)-1]; got != env.sessionAccessToken {
				t.Fatalf("the session token must ride the hop, got %q", got)
			}
		})
	}
}

func TestUnitCollectionPassThroughs_UpstreamFailureIs502(t *testing.T) {
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{}, collectionclient.ErrUpstream
	}}
	h, env := newTestHandlersWithCollection(t, col)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/dashboard")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d", rec.Code)
	}
}

// TestUnitCollectionReorderPassThrough_RelaysProblemBody mirrors
// TestUnitProductPassThrough_RelaysProblemBody for a collection route:
// a conflict from the collection service (two backlog items racing for
// the same rank) must relay verbatim - status, content type, and body -
// exactly like every other pass-through, never rewritten by the bff.
func TestUnitCollectionReorderPassThrough_RelaysProblemBody(t *testing.T) {
	const problemBody = `{"type":"about:blank","title":"Conflict","status":409,"code":"conflicting_order"}`
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 409, ContentType: "application/problem+json",
			Body: []byte(problemBody)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	id := uuid.NewString()
	req := httptest.NewRequest(http.MethodPost, "/api/entries/"+id+"/reorder", strings.NewReader(`{"after_id":null}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != 409 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("problem relay: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != problemBody {
		t.Fatalf("body must relay verbatim, got %s", rec.Body.String())
	}
}

// TestUnitValueHistoryPassThrough proves the value-history route relays
// the collection service's body verbatim and forwards the session's
// own bearer, exactly like the other collection pass-throughs.
func TestUnitValueHistoryPassThrough(t *testing.T) {
	relayed := []byte(`{"available":true,"points":[{"date":"2026-07-01","value_cents":4200}]}`)
	col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
		if op != "value_history" {
			t.Fatalf("routed to %q, want value_history", op)
		}
		return collectionclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: relayed}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/dashboard/value-history")
	if rec.Code != http.StatusOK || rec.Body.String() != string(relayed) {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if got := col.gotBearer[len(col.gotBearer)-1]; got != env.sessionAccessToken {
		t.Fatalf("the session token must ride the hop, got %q", got)
	}
}

// captureCollection embeds the stub so every method forwards, while
// ListEntries, GetDashboard, and UpdateEntry additionally expose their
// converted params / raw body.
type captureCollection struct {
	*stubCollection
	onList        func(*collectionapi.ListEntriesParams)
	onDashboard   func(*collectionapi.GetDashboardParams)
	onUpdateEntry func(id uuid.UUID, body []byte)
}

func (c *captureCollection) ListEntries(ctx context.Context, bearer string, p *collectionapi.ListEntriesParams) (collectionclient.Result, error) {
	c.onList(p)
	return c.stubCollection.ListEntries(ctx, bearer, p)
}

func (c *captureCollection) GetDashboard(ctx context.Context, bearer string, p *collectionapi.GetDashboardParams) (collectionclient.Result, error) {
	c.onDashboard(p)
	return c.stubCollection.GetDashboard(ctx, bearer, p)
}

func (c *captureCollection) UpdateEntry(ctx context.Context, bearer string, id uuid.UUID, body []byte) (collectionclient.Result, error) {
	c.onUpdateEntry(id, body)
	return c.stubCollection.UpdateEntry(ctx, bearer, id, body)
}

func TestUnitCollectionListParams_Conversion(t *testing.T) {
	var got *collectionapi.ListEntriesParams
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{}`)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	h.collection = &captureCollection{stubCollection: col, onList: func(p *collectionapi.ListEntriesParams) { got = p }}
	rec := doAuthed(t, h, env, http.MethodGet,
		"/api/entries?status=backlog&status=playing&sort=value&order=desc&group_by=platform&platform_id=6")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if got == nil || got.Status == nil || len(*got.Status) != 2 ||
		string((*got.Status)[0]) != "backlog" || string(*got.Sort) != "value" ||
		string(*got.Order) != "desc" || string(*got.GroupBy) != "platform" ||
		(*got.PlatformId)[0] != 6 {
		t.Fatalf("converted params: %+v", got)
	}
}

func TestUnitDashboardParams_Forwarded(t *testing.T) {
	var got *collectionapi.GetDashboardParams
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{}`)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	h.collection = &captureCollection{stubCollection: col, onDashboard: func(p *collectionapi.GetDashboardParams) { got = p }}
	rec := doAuthed(t, h, env, http.MethodGet,
		"/api/dashboard?status=backlog&item_type=game&platform_id=6")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if got == nil || got.Status == nil || len(*got.Status) != 1 ||
		string((*got.Status)[0]) != "backlog" ||
		got.ItemType == nil || string((*got.ItemType)[0]) != "game" ||
		got.PlatformId == nil || (*got.PlatformId)[0] != 6 {
		t.Fatalf("forwarded params: %+v", got)
	}
}

// TestUnitUpdateEntryPassThrough_CustomPricingRoundTrips pins that a
// pricing_mode=custom entry update reaches the collection stub with
// its body unmodified, and the stub's answer - carrying
// custom_value_cents/custom_value_set_at - round-trips to the client
// verbatim. The bff neither validates nor reshapes custom pricing; it
// is a pass-through like every other entry mutation.
func TestUnitUpdateEntryPassThrough_CustomPricingRoundTrips(t *testing.T) {
	id := uuid.New()
	const sent = `{"pricing_mode":"custom","custom_value_cents":12345}`
	relayed := []byte(`{"id":"` + id.String() + `","pricing_mode":"custom","custom_value_cents":12345,"custom_value_set_at":"2026-07-09T00:00:00Z"}`)

	col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
		if op != "update_entry" {
			t.Fatalf("routed to %q, want update_entry", op)
		}
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: relayed}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	var gotID uuid.UUID
	var gotBody []byte
	h.collection = &captureCollection{stubCollection: col, onUpdateEntry: func(recvID uuid.UUID, body []byte) {
		gotID, gotBody = recvID, body
	}}

	req := httptest.NewRequest(http.MethodPut, "/api/entries/"+id.String(), strings.NewReader(sent))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)

	if gotID != id || string(gotBody) != sent {
		t.Fatalf("collection saw id=%s body=%s, want id=%s body=%s", gotID, gotBody, id, sent)
	}
	if rec.Code != 200 || rec.Body.String() != string(relayed) {
		t.Fatalf("relay: %d %s, want 200 %s", rec.Code, rec.Body.String(), relayed)
	}
}

// TestUnitUpdateEntryPassThrough_CustomPricingEnteredPairRoundTrips pins
// that the typed custom-price pair (custom_value_entered_cents /
// custom_value_entered_currency) rides an entry update through the bff
// exactly like every other custom pricing field: unmodified in, and the
// collection stub's answer - carrying the pair - relays to the client
// verbatim. The bff neither validates nor reshapes it.
func TestUnitUpdateEntryPassThrough_CustomPricingEnteredPairRoundTrips(t *testing.T) {
	id := uuid.New()
	const sent = `{"pricing_mode":"custom","custom_value_cents":5400,"custom_value_entered_cents":6000,"custom_value_entered_currency":"EUR"}`
	const relayed = `{"id":"e1","custom_value_cents":5400,"custom_value_entered_cents":6000,"custom_value_entered_currency":"EUR","pricing_mode":"custom"}`

	col := &stubCollection{answer: func(op string) (collectionclient.Result, error) {
		if op != "update_entry" {
			t.Fatalf("routed to %q, want update_entry", op)
		}
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(relayed)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	var gotID uuid.UUID
	var gotBody []byte
	h.collection = &captureCollection{stubCollection: col, onUpdateEntry: func(recvID uuid.UUID, body []byte) {
		gotID, gotBody = recvID, body
	}}

	req := httptest.NewRequest(http.MethodPut, "/api/entries/"+id.String(), strings.NewReader(sent))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)

	if gotID != id || string(gotBody) != sent {
		t.Fatalf("collection saw id=%s body=%s, want id=%s body=%s", gotID, gotBody, id, sent)
	}
	if rec.Code != 200 || rec.Body.String() != relayed {
		t.Fatalf("relay: %d %s, want 200 %s", rec.Code, rec.Body.String(), relayed)
	}
}

func TestUnitRecommendations_ComposesAndCaches(t *testing.T) {
	scoreBody := []byte(`{"degraded":false,"recommendations":[{"igdb_game_id":9,"name":"Alundra","genres":["RPG"],"score":4.2}]}`)
	rating := 8
	var scoreCalls int
	var gotReq enrichapi.ScoreRequest
	col := &stubCollection{library: func(_ context.Context, bearer string) (collectionapi.LibrarySummary, error) {
		dropped := "dropped"
		return collectionapi.LibrarySummary{Library: []collectionapi.LibraryGame{
			{IgdbGameId: 1000, Rating: &rating},
			{IgdbGameId: 1001, Status: &dropped},
		}}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	h.enrichment = &stubEnrichment{score: func(_ context.Context, _ string, req enrichapi.ScoreRequest) ([]byte, bool, error) {
		scoreCalls++
		gotReq = req
		return scoreBody, false, nil
	}}

	rec := doAuthed(t, h, env, http.MethodGet, "/api/recommendations")
	if rec.Code != 200 || rec.Body.String() != string(scoreBody) {
		t.Fatalf("compose: %d %s", rec.Code, rec.Body.String())
	}
	// The library piped through unshaped.
	if len(gotReq.Library) != 2 || gotReq.Library[0].IgdbGameId != 1000 ||
		*gotReq.Library[0].Rating != 8 || *gotReq.Library[1].Status != "dropped" {
		t.Fatalf("score request: %+v", gotReq.Library)
	}
	// The second read is a cache hit: no second score call.
	rec = doAuthed(t, h, env, http.MethodGet, "/api/recommendations")
	if rec.Code != 200 || scoreCalls != 1 {
		t.Fatalf("cache hit: %d calls=%d", rec.Code, scoreCalls)
	}
}

func TestUnitRecommendations_DegradedIsNotCached(t *testing.T) {
	col := &stubCollection{library: func(context.Context, string) (collectionapi.LibrarySummary, error) {
		return collectionapi.LibrarySummary{Library: []collectionapi.LibraryGame{}}, nil
	}}
	var scoreCalls int
	h, env := newTestHandlersWithCollection(t, col)
	h.enrichment = &stubEnrichment{score: func(context.Context, string, enrichapi.ScoreRequest) ([]byte, bool, error) {
		scoreCalls++
		return []byte(`{"degraded":true,"recommendations":[]}`), true, nil
	}}
	doAuthed(t, h, env, http.MethodGet, "/api/recommendations")
	doAuthed(t, h, env, http.MethodGet, "/api/recommendations")
	if scoreCalls != 2 {
		t.Fatalf("a degraded score must not be cached (calls=%d)", scoreCalls)
	}
}

func TestUnitRecommendations_UpstreamFailures(t *testing.T) {
	t.Run("collection down", func(t *testing.T) {
		col := &stubCollection{library: func(context.Context, string) (collectionapi.LibrarySummary, error) {
			return collectionapi.LibrarySummary{}, collectionclient.ErrUpstream
		}}
		h, env := newTestHandlersWithCollection(t, col)
		if rec := doAuthed(t, h, env, http.MethodGet, "/api/recommendations"); rec.Code != http.StatusBadGateway {
			t.Fatalf("status %d", rec.Code)
		}
	})
	t.Run("enrichment down", func(t *testing.T) {
		col := &stubCollection{library: func(context.Context, string) (collectionapi.LibrarySummary, error) {
			return collectionapi.LibrarySummary{Library: []collectionapi.LibraryGame{}}, nil
		}}
		h, env := newTestHandlersWithCollection(t, col)
		h.enrichment = &stubEnrichment{score: func(context.Context, string, enrichapi.ScoreRequest) ([]byte, bool, error) {
			return nil, false, enrichmentclient.ErrUpstream
		}}
		if rec := doAuthed(t, h, env, http.MethodGet, "/api/recommendations"); rec.Code != http.StatusBadGateway {
			t.Fatalf("status %d", rec.Code)
		}
	})
}

func TestUnitEntryMutationInvalidatesRecs(t *testing.T) {
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 201, ContentType: "application/json", Body: []byte(`{}`)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, col)
	sc := h.cache.(*stubCache)
	// Pre-seed a cached recommendation body for this session's subject.
	sub := subjectOf(t, env.sessionAccessToken)
	sc.recs[sub] = []byte(`{"degraded":false,"recommendations":[]}`)

	req := httptest.NewRequest(http.MethodPost, "/api/entries", strings.NewReader(`{"product_id":"x"}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("mutation: %d", rec.Code)
	}
	if sc.recs[sub] != nil {
		t.Fatal("a successful entry mutation must invalidate the recommendations cache")
	}
}

// subjectOf decodes the sub claim from an unverified test token.
func subjectOf(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims.Sub
}

// stubRoundTripper lets a test assert whether the otlp relay's upstream
// http.Client was ever dialed.
type stubRoundTripper struct {
	fn func(*http.Request) (*http.Response, error)
}

func (s *stubRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return s.fn(r) }

// newTestHandlersForOtlp builds Handlers with a fresh session ready to
// drive the otlp relay route; otlpProxyURL configures the relay target
// ("" is drop mode).
func newTestHandlersForOtlp(t *testing.T, otlpProxyURL string) (*Handlers, *testEnv) {
	t.Helper()
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
	h.otlpProxyURL = otlpProxyURL
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
}

func TestUnitProxyTraces_RequiresSession(t *testing.T) {
	h, env := newTestHandlersForOtlp(t, "")
	rec := doUnauthed(t, h, env, http.MethodPost, "/api/otlp/v1/traces")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}
}

func TestUnitProxyTraces_DropModeAnswers200(t *testing.T) {
	h, env := newTestHandlersForOtlp(t, "")
	h.otlpHTTP = &http.Client{Transport: &stubRoundTripper{fn: func(*http.Request) (*http.Response, error) {
		t.Fatal("drop mode must never dial the upstream")
		return nil, nil
	}}}
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(`{"resourceSpans":[]}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Fatalf("drop mode: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// TestUnitProxyTraces_RelaysVerbatim proves the relay is a pure pass-
// through in both directions: the collector sees the exact method, path,
// body, and Content-Type/Content-Encoding the browser sent (but never the
// session cookie), and the browser sees the collector's exact status,
// content type, and body back.
func TestUnitProxyTraces_RelaysVerbatim(t *testing.T) {
	const marker = "collector-response-marker"
	const sentBody = `{"resourceSpans":[{"resource":{}}]}`
	var gotMethod, gotPath, gotContentType, gotContentEncoding, gotCookie string
	var gotBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotContentEncoding = r.Header.Get("Content-Encoding")
		gotCookie = r.Header.Get("Cookie")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(marker))
	}))
	defer upstream.Close()

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(sentBody))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-protobuf" || rec.Body.String() != marker {
		t.Fatalf("relay to caller: code=%d content-type=%q body=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/traces" {
		t.Fatalf("upstream request line: method=%s path=%s", gotMethod, gotPath)
	}
	if string(gotBody) != sentBody {
		t.Fatalf("upstream body = %q, want %q", gotBody, sentBody)
	}
	if gotContentType != "application/json" || gotContentEncoding != "gzip" {
		t.Fatalf("forwarded headers: content-type=%q content-encoding=%q", gotContentType, gotContentEncoding)
	}
	if gotCookie != "" {
		t.Fatalf("the session cookie must never ride the upstream hop, got %q", gotCookie)
	}
}

func TestUnitProxyTraces_UpstreamPartialStatusPassesThrough(t *testing.T) {
	const problemBody = `{"type":"about:blank","title":"Bad Request","status":400,"code":"bad_export"}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(problemBody))
	}))
	defer upstream.Close()

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(`{}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || rec.Body.String() != problemBody {
		t.Fatalf("partial status relay: code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUnitProxyTraces_UpstreamDownAnswers502(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	upstream.Close() // closed before any request: dialing it must fail

	h, env := newTestHandlersForOtlp(t, upstream.URL)
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(`{}`))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("upstream down: code = %d", rec.Code)
	}
}

func TestUnitProxyTraces_OversizeBodyAnswers400(t *testing.T) {
	h, env := newTestHandlersForOtlp(t, "") // drop mode: the cap check must still run first
	req := httptest.NewRequest(http.MethodPost, "/api/otlp/v1/traces", strings.NewReader(strings.Repeat("a", 1<<20+1)))
	req.AddCookie(env.cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversize body: code = %d", rec.Code)
	}
}

func TestUnitAdminWorklist_RelaysAndForwardsParams(t *testing.T) {
	const page = `{"products":[],"total_count":0}`
	var gotBearer string
	var gotParams *enrichapi.ListUnmatchedProductsParams
	enrich := &stubEnrichment{unmatchedProducts: func(_ context.Context, bearer string, params *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error) {
		gotBearer, gotParams = bearer, params
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(page)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/unmatched?limit=5&offset=10")
	if rec.Code != 200 || rec.Body.String() != page {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}
	if gotParams == nil || gotParams.Limit == nil || *gotParams.Limit != 5 || gotParams.Offset == nil || *gotParams.Offset != 10 {
		t.Fatalf("params passthrough: %+v", gotParams)
	}
}

func TestUnitAdminWorklist_Forbidden403RelaysVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Forbidden","status":403,"code":"forbidden","detail":"role admin required"}`
	enrich := &stubEnrichment{unmatchedProducts: func(context.Context, string, *enrichapi.ListUnmatchedProductsParams) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 403, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/unmatched")
	if rec.Code != 403 || rec.Body.String() != problem {
		t.Fatalf("403 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type: %q", ct)
	}
}

func TestUnitCommunityWorklist_RelaysAndForwardsParams(t *testing.T) {
	const page = `{"products":[],"total_count":0}`
	var gotBearer string
	var gotParams *enrichapi.ListCommunityProductsParams
	enrich := &stubEnrichment{communityProducts: func(_ context.Context, bearer string, params *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error) {
		gotBearer, gotParams = bearer, params
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(page)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/community?limit=5&offset=10")
	if rec.Code != 200 || rec.Body.String() != page {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}
	if gotParams == nil || gotParams.Limit == nil || *gotParams.Limit != 5 || gotParams.Offset == nil || *gotParams.Offset != 10 {
		t.Fatalf("params passthrough: %+v", gotParams)
	}
}

func TestUnitCommunityWorklist_Forbidden403RelaysVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Forbidden","status":403,"code":"forbidden","detail":"role admin required"}`
	enrich := &stubEnrichment{communityProducts: func(context.Context, string, *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 403, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/community")
	if rec.Code != 403 || rec.Body.String() != problem {
		t.Fatalf("403 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type: %q", ct)
	}
}

func TestUnitCommunityWorklist_ClientErrorAnswers502(t *testing.T) {
	enrich := &stubEnrichment{communityProducts: func(context.Context, string, *enrichapi.ListCommunityProductsParams) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/community")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

func TestUnitAdminMapping_RelaysBodyAndConflict(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Conflict","status":409,"code":"identity_taken","detail":"another product with the same identity already carries that listing"}`
	id := uuid.New()
	var gotBody []byte
	enrich := &stubEnrichment{setProductMapping: func(_ context.Context, _ string, gotID uuid.UUID, body []byte) (enrichmentclient.Result, error) {
		if gotID != id {
			t.Errorf("id = %v", gotID)
		}
		gotBody = body
		return enrichmentclient.Result{Status: 409, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	r := httptest.NewRequest(http.MethodPut, "/api/admin/products/"+id.String()+"/pricecharting", strings.NewReader(`{"pc_product_id":5005}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(env.cookie)
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != 409 || rec.Body.String() != problem {
		t.Fatalf("409 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if string(gotBody) != `{"pc_product_id":5005}` {
		t.Fatalf("body passthrough: %s", gotBody)
	}
}

func TestUnitAdminRefresh_Relays202(t *testing.T) {
	const accepted = `{"status":"started"}`
	enrich := &stubEnrichment{triggerRefresh: func(_ context.Context, bearer string) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{Status: 202, ContentType: "application/json", Body: []byte(accepted)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthed(t, h, env, http.MethodPost, "/api/admin/refresh")
	if rec.Code != 202 || rec.Body.String() != accepted {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnitAdminRoutes_NoSession401(t *testing.T) {
	h, env := newTestHandlersWithEnrichment(t, &stubEnrichment{})
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/products/unmatched"},
		{http.MethodGet, "/api/admin/products/community"},
		{http.MethodPut, "/api/admin/products/" + uuid.NewString() + "/pricecharting"},
		{http.MethodPost, "/api/admin/refresh"},
	} {
		rec := doUnauthed(t, h, env, tc.method, tc.path)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: %d", tc.method, tc.path, rec.Code)
		}
	}
}

func TestUnitAdminDelete_ReferencedAnswers409BeforeEnrichment(t *testing.T) {
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{"entry_count":3}`)}, nil
	}}
	// deleteProduct stays nil: reaching enrichment would panic, which
	// is the ordering assertion.
	h, env := newTestHandlersWithEnrichment(t, &stubEnrichment{})
	h.collection = col
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/admin/products/"+uuid.NewString(), nil)
	r.AddCookie(env.cookie)
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != http.StatusConflict {
		t.Fatalf("referenced delete: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "product_referenced") || !strings.Contains(rec.Body.String(), "3 entries") {
		t.Fatalf("problem must carry the code and count: %s", rec.Body.String())
	}
}

func TestUnitAdminDelete_UnreferencedRelaysEnrichment(t *testing.T) {
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(`{"entry_count":0}`)}, nil
	}}
	var gotBearer string
	enrich := &stubEnrichment{deleteProduct: func(_ context.Context, bearer string, _ uuid.UUID) (enrichmentclient.Result, error) {
		gotBearer = bearer
		return enrichmentclient.Result{Status: http.StatusNoContent}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	h.collection = col
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/admin/products/"+uuid.NewString(), nil)
	r.AddCookie(env.cookie)
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unreferenced delete: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer reaching enrichment: %q", gotBearer)
	}
}

func TestUnitAdminDelete_Collection403RelaysVerbatim(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Forbidden","status":403,"code":"forbidden","detail":"role admin required"}`
	col := &stubCollection{answer: func(string) (collectionclient.Result, error) {
		return collectionclient.Result{Status: 403, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, &stubEnrichment{})
	h.collection = col
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodDelete, "/api/admin/products/"+uuid.NewString(), nil)
	r.AddCookie(env.cookie)
	newRouterFor(t, h).ServeHTTP(rec, r)
	if rec.Code != 403 || rec.Body.String() != problem {
		t.Fatalf("collection 403 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnitAdminDelete_NoSession401(t *testing.T) {
	h, env := newTestHandlersWithEnrichment(t, &stubEnrichment{})
	rec := doUnauthed(t, h, env, http.MethodDelete, "/api/admin/products/"+uuid.NewString())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

// TestUnitSubmissionRelays_FidelityAndNoSession covers the three user
// submission ops: a create relays the 201 body verbatim and forwards
// the session's own bearer, a read relays a problem body verbatim,
// and a mutation with no session answers 401 before the handler runs.
func TestUnitSubmissionRelays_FidelityAndNoSession(t *testing.T) {
	const sub = `{"id":"s1","entry_id":"e1","status":"pending","created_at":"2026-07-17T00:00:00Z","updated_at":"2026-07-17T00:00:00Z"}`
	var gotBearer string
	coll := &stubCollection{
		createSubmission: func(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
			gotBearer = bearer
			return collectionclient.Result{Status: 201, ContentType: "application/json", Body: []byte(sub)}, nil
		},
		getSubmission: func(context.Context, string, uuid.UUID) (collectionclient.Result, error) {
			return collectionclient.Result{Status: 404, ContentType: "application/problem+json",
				Body: []byte(`{"type":"about:blank","title":"Not Found","status":404,"code":"submission_not_found"}`)}, nil
		},
	}
	h, env := newTestHandlersWithCollection(t, coll)
	entry := uuid.NewString()

	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/entries/"+entry+"/submission", "")
	if rec.Code != 201 || rec.Body.String() != sub {
		t.Fatalf("create relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}

	rec = doAuthed(t, h, env, http.MethodGet, "/api/entries/"+entry+"/submission")
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "submission_not_found") {
		t.Fatalf("problem relay: %d %s", rec.Code, rec.Body.String())
	}

	rec = doUnauthed(t, h, env, http.MethodPost, "/api/entries/"+entry+"/submission")
	if rec.Code != 401 {
		t.Fatalf("no session: %d", rec.Code)
	}
}

// TestUnitVerdictRelay_BodyPassthroughAnd409 proves the admin verdict
// forwards the browser's body untouched and relays a conflict
// (another admin already resolved the row) verbatim.
func TestUnitVerdictRelay_BodyPassthroughAnd409(t *testing.T) {
	var gotBody []byte
	coll := &stubCollection{submitVerdict: func(_ context.Context, _ string, _ uuid.UUID, body []byte) (collectionclient.Result, error) {
		gotBody = body
		return collectionclient.Result{Status: 409, ContentType: "application/problem+json",
			Body: []byte(`{"type":"about:blank","title":"Conflict","status":409,"code":"submission_resolved"}`)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, coll)

	payload := `{"action":"reject","reason":"not shared"}`
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/submissions/"+uuid.NewString()+"/verdict", payload)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "submission_resolved") {
		t.Fatalf("verdict relay: %d %s", rec.Code, rec.Body.String())
	}
	if string(gotBody) != payload {
		t.Fatalf("body must pass through untouched: %s", gotBody)
	}
}

// TestUnitPromoteRelays_ParamsAndConflict proves the candidates read
// forwards its query params (limit/offset/product_id) and the promote
// mutation relays a conflict (a provider twin already holds the
// identity) verbatim.
func TestUnitPromoteRelays_ParamsAndConflict(t *testing.T) {
	var gotParams *enrichapi.ListPromoteCandidatesParams
	enrich := &stubEnrichment{
		promoteCandidates: func(_ context.Context, _ string, params *enrichapi.ListPromoteCandidatesParams) (enrichmentclient.Result, error) {
			gotParams = params
			return enrichmentclient.Result{Status: 200, ContentType: "application/json",
				Body: []byte(`{"products":[],"total_count":0}`)}, nil
		},
		promoteProduct: func(context.Context, string, uuid.UUID, []byte) (enrichmentclient.Result, error) {
			return enrichmentclient.Result{Status: 409, ContentType: "application/problem+json",
				Body: []byte(`{"type":"about:blank","title":"Conflict","status":409,"code":"identity_taken"}`)}, nil
		},
	}
	h, env := newTestHandlersWithEnrichment(t, enrich)

	pid := uuid.NewString()
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/products/promote-candidates?limit=5&offset=10&product_id="+pid)
	if rec.Code != 200 {
		t.Fatalf("candidates relay: %d", rec.Code)
	}
	if gotParams == nil || gotParams.Limit == nil || *gotParams.Limit != 5 || gotParams.ProductId == nil {
		t.Fatalf("params passthrough: %+v", gotParams)
	}

	rec = doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products/"+pid+"/promote", `{"igdb_game_id":1011,"platform_igdb_id":19}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "identity_taken") {
		t.Fatalf("promote conflict relay: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitCancelSubmission_RelaysAndForwardsBearer proves the pending-
// submission cancel forwards the session's own bearer and relays the
// upstream's answer verbatim; a request with no session never reaches
// the handler.
func TestUnitCancelSubmission_RelaysAndForwardsBearer(t *testing.T) {
	var gotBearer string
	coll := &stubCollection{cancelSubmission: func(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
		gotBearer = bearer
		return collectionclient.Result{Status: http.StatusNoContent}, nil
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	entry := uuid.NewString()

	rec := doAuthed(t, h, env, http.MethodDelete, "/api/entries/"+entry+"/submission")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("cancel relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}

	rec = doUnauthed(t, h, env, http.MethodDelete, "/api/entries/"+entry+"/submission")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

// TestUnitListSubmissions_RelaysAndForwardsParams proves the admin
// queue read forwards its query params (limit/offset) and relays the
// upstream body verbatim; collection enforces the role, so the bff
// holds no gate of its own here beyond the session.
func TestUnitListSubmissions_RelaysAndForwardsParams(t *testing.T) {
	const page = `{"submissions":[],"total_count":0}`
	var gotBearer string
	var gotParams *collectionapi.ListSubmissionsParams
	coll := &stubCollection{listSubmissions: func(_ context.Context, bearer string, params *collectionapi.ListSubmissionsParams) (collectionclient.Result, error) {
		gotBearer, gotParams = bearer, params
		return collectionclient.Result{Status: 200, ContentType: "application/json", Body: []byte(page)}, nil
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/submissions?limit=5&offset=10")
	if rec.Code != 200 || rec.Body.String() != page {
		t.Fatalf("relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}
	if gotParams == nil || gotParams.Limit == nil || *gotParams.Limit != 5 || gotParams.Offset == nil || *gotParams.Offset != 10 {
		t.Fatalf("params passthrough: %+v", gotParams)
	}
}

// TestUnitCreateCommunityProduct_RelaysBodyAndForbidden proves the
// admin mint forwards the browser's body untouched and relays
// enrichment's role refusal verbatim (enrichment enforces admin, the
// bff holds no role logic of its own on admin routes).
func TestUnitCreateCommunityProduct_RelaysBodyAndForbidden(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Forbidden","status":403,"code":"forbidden","detail":"role admin required"}`
	var gotBody []byte
	enrich := &stubEnrichment{createCommunityProduct: func(_ context.Context, _ string, body []byte) (enrichmentclient.Result, error) {
		gotBody = body
		return enrichmentclient.Result{Status: 403, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	payload := `{"name":"Homebrew Cart","type":"game"}`
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products", payload)
	if rec.Code != 403 || rec.Body.String() != problem {
		t.Fatalf("403 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if string(gotBody) != payload {
		t.Fatalf("body passthrough: %s", gotBody)
	}
}

// TestUnitDismissPromoteCandidate_RelaysBodyAndNotFound proves the
// candidate dismissal forwards the target id and the browser's body
// untouched, and relays a not-found verbatim (the candidate left the
// sweep worklist between page load and dismiss).
func TestUnitDismissPromoteCandidate_RelaysBodyAndNotFound(t *testing.T) {
	const problem = `{"type":"about:blank","title":"Not Found","status":404,"code":"not_found"}`
	pid := uuid.New()
	var gotID uuid.UUID
	var gotBody []byte
	enrich := &stubEnrichment{dismissPromoteCandidate: func(_ context.Context, _ string, id uuid.UUID, body []byte) (enrichmentclient.Result, error) {
		gotID, gotBody = id, body
		return enrichmentclient.Result{Status: 404, ContentType: "application/problem+json", Body: []byte(problem)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	payload := `{"provider":"pricecharting","provider_id":5005}`
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products/"+pid.String()+"/promote-candidates/dismiss", payload)
	if rec.Code != 404 || rec.Body.String() != problem {
		t.Fatalf("404 must relay verbatim: %d %s", rec.Code, rec.Body.String())
	}
	if gotID != pid || string(gotBody) != payload {
		t.Fatalf("id/body passthrough: id=%s body=%s", gotID, gotBody)
	}
}

// The four tests below mirror TestUnitFxRelay_ClientErrorAnswers502: each
// covers one new handler's own upstream-failure branch (a dead client,
// not an upstream answer) which no other test above happens to exercise.

func TestUnitCancelSubmission_ClientErrorAnswers502(t *testing.T) {
	coll := &stubCollection{cancelSubmission: func(context.Context, string, uuid.UUID) (collectionclient.Result, error) {
		return collectionclient.Result{}, collectionclient.ErrUpstream
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	rec := doAuthed(t, h, env, http.MethodDelete, "/api/entries/"+uuid.NewString()+"/submission")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

func TestUnitListSubmissions_ClientErrorAnswers502(t *testing.T) {
	coll := &stubCollection{listSubmissions: func(context.Context, string, *collectionapi.ListSubmissionsParams) (collectionclient.Result, error) {
		return collectionclient.Result{}, collectionclient.ErrUpstream
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	rec := doAuthed(t, h, env, http.MethodGet, "/api/admin/submissions")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

func TestUnitCreateCommunityProduct_ClientErrorAnswers502(t *testing.T) {
	enrich := &stubEnrichment{createCommunityProduct: func(context.Context, string, []byte) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products", `{"name":"Homebrew Cart","type":"game"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

func TestUnitDismissPromoteCandidate_ClientErrorAnswers502(t *testing.T) {
	enrich := &stubEnrichment{dismissPromoteCandidate: func(context.Context, string, uuid.UUID, []byte) (enrichmentclient.Result, error) {
		return enrichmentclient.Result{}, enrichmentclient.ErrUpstream
	}}
	h, env := newTestHandlersWithEnrichment(t, enrich)
	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/admin/products/"+uuid.NewString()+"/promote-candidates/dismiss", `{"provider":"pricecharting","provider_id":5005}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_error") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}

func TestUnitListPlatforms_RelaysAndForwardsBearer(t *testing.T) {
	const body = `{"platforms":[{"igdb_id":19,"name":"Super Nintendo Entertainment System","aliases":["snes"]}]}`
	var gotBearer string
	enr := &stubEnrichment{listPlatforms: func(_ context.Context, bearer string) (enrichmentclient.Result, error) {
		gotBearer = bearer
		return enrichmentclient.Result{Status: 200, ContentType: "application/json", Body: []byte(body)}, nil
	}}
	h, env := newTestHandlersWithEnrichment(t, enr)

	rec := doAuthed(t, h, env, http.MethodGet, "/api/platforms")
	if rec.Code != 200 || rec.Body.String() != body {
		t.Fatalf("platforms relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}
	rec = doUnauthed(t, h, env, http.MethodGet, "/api/platforms")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

func TestUnitAckSubmission_RelaysAndForwardsBearer(t *testing.T) {
	var gotBearer string
	coll := &stubCollection{ackSubmission: func(_ context.Context, bearer string, _ uuid.UUID) (collectionclient.Result, error) {
		gotBearer = bearer
		return collectionclient.Result{Status: http.StatusNoContent}, nil
	}}
	h, env := newTestHandlersWithCollection(t, coll)
	entry := uuid.NewString()

	rec := doAuthedBody(t, h, env, http.MethodPost, "/api/entries/"+entry+"/submission/ack", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ack relay: %d %s", rec.Code, rec.Body.String())
	}
	if gotBearer != env.sessionAccessToken {
		t.Fatalf("bearer: %q", gotBearer)
	}
	rec = doUnauthed(t, h, env, http.MethodPost, "/api/entries/"+entry+"/submission/ack")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}
