// Tests for the signed-in user's own profile: read, update,
// linked-identity management, and account deletion.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/contract/userapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
	"github.com/levonn-dev/vgkeep/services/bff/internal/socialclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
)

func TestGetMe(t *testing.T) {
	uid := uuid.New()
	fc := newStubCache()
	avatar := "https://cdn.example/a.png"
	h := newTestHandlers(t, fc, &stubAuth{})
	h.users = &stubUsers{get: func(context.Context, string, string) (userapi.User, error) {
		return userapi.User{
			Id: uid, Email: "alice@example.test", Handle: "alice",
			AvatarUrl: &avatar, Roles: []common.Role{"user"},
		}, nil
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

	// Second call served from cache: an unconfigured user client would
	// panic if reached, proving it never is.
	h.users = &stubUsers{}
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	router.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached call: code = %d", rec.Code)
	}
}

func TestGetMeUserGone(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.users = &stubUsers{get: func(context.Context, string, string) (userapi.User, error) {
		return userapi.User{}, userclient.ErrUserNotFound
	}}
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
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.users = &stubUsers{get: func(context.Context, string, string) (userapi.User, error) {
		return userapi.User{}, errors.New("user service down")
	}}
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
	s.users.id, s.users.email, s.users.handle = sub, "alice@example.test", "alice"
	access := mintAccess(t, sub, "jA", time.Now().Add(5*time.Minute)) // fresh: no refresh
	cookie := s.cookieFor(t, access, "refresh-1")

	r1 := s.getMe(t, cookie)
	if r1.status != http.StatusOK {
		t.Fatalf("compose call: status = %d body=%s", r1.status, r1.body)
	}
	var me struct {
		Id     string   `json:"id"`
		Email  string   `json:"email"`
		Handle string   `json:"handle"`
		Roles  []string `json:"roles"`
	}
	if err := json.Unmarshal(r1.body, &me); err != nil {
		t.Fatalf("compose body: %v (%s)", err, r1.body)
	}
	if me.Id != sub || me.Email != "alice@example.test" || me.Handle != "alice" ||
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
		userJSON := []byte(`{"id":"` + uid.String() + `","email":"alice@example.test","handle":"alice2","roles":["user"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`)
		fc := newStubCache()
		fc.me[uid.String()] = []byte(`{"stale":true}`)
		h := newTestHandlers(t, fc, &stubAuth{})
		h.users = &stubUsers{update: func(context.Context, string, string, []byte) (userclient.Result, error) {
			return userclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: userJSON}, nil
		}}
		access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodPatch, "/api/me", strings.NewReader(`{"handle":"alice2"}`))
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
		if me.Id != uid || me.Email != "alice@example.test" || me.Handle != "alice2" ||
			len(me.Roles) != 1 || me.Roles[0] != "user" {
			t.Fatalf("me = %+v", me)
		}
		if fc.me[uid.String()] != nil {
			t.Fatal("a successful update must invalidate the /api/me cache")
		}
	})

	t.Run("400_relays_verbatim_and_does_not_invalidate", func(t *testing.T) {
		problemJSON := []byte(`{"type":"about:blank","title":"Bad Request","status":400,"code":"invalid_body","detail":"handle too long"}`)
		fc := newStubCache()
		fc.me[uid.String()] = []byte(`{"cached":true}`)
		h := newTestHandlers(t, fc, &stubAuth{})
		h.users = &stubUsers{update: func(context.Context, string, string, []byte) (userclient.Result, error) {
			return userclient.Result{Status: http.StatusBadRequest, ContentType: "application/problem+json", Body: problemJSON}, nil
		}}
		access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodPatch, "/api/me", strings.NewReader(`{"handle":"alice3"}`))
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
		h := newTestHandlers(t, newStubCache(), &stubAuth{})
		h.users = &stubUsers{update: func(context.Context, string, string, []byte) (userclient.Result, error) {
			return userclient.Result{}, errors.New("user service down")
		}}
		access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodPatch, "/api/me", strings.NewReader(`{"handle":"alice4"}`))
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
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.users = &stubUsers{get: func(context.Context, string, string) (userapi.User, error) {
		return userapi.User{
			Id: uid, Email: "alice@example.test", Handle: "alice",
			Roles: []common.Role{"user"}, PreferredCurrency: "EUR",
		}, nil
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

// TestUnitGetMe_IncludesProfileVisibility pins that a non-default
// profile_visibility reaches the browser projection unchanged (the
// user service's column default is "private"; a silently-dropped or
// defaulted-away field would still read "private" here).
func TestUnitGetMe_IncludesProfileVisibility(t *testing.T) {
	t.Run("non_default_value_round_trips", func(t *testing.T) {
		uid := uuid.New()
		h := newTestHandlers(t, newStubCache(), &stubAuth{})
		h.users = &stubUsers{get: func(context.Context, string, string) (userapi.User, error) {
			return userapi.User{
				Id: uid, Email: "alice@example.test", Handle: "alice",
				Roles: []common.Role{"user"}, ProfileVisibility: common.Listed,
			}, nil
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
			ProfileVisibility string `json:"profile_visibility"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.ProfileVisibility != "listed" {
			t.Fatalf("profile_visibility: %q, want listed (the user service default is private)", got.ProfileVisibility)
		}
	})
}

// TestUnitGetMe_IncludesLandingPage pins that the profile's landing_page
// preference reaches the browser projection (the user service default
// is "feed"; a non-default value proves the field is not silently
// dropped or defaulted-away in the composition).
func TestUnitGetMe_IncludesLandingPage(t *testing.T) {
	uid := uuid.New()
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.users = &stubUsers{get: func(context.Context, string, string) (userapi.User, error) {
		return userapi.User{
			Id: uid, Email: "alice@example.test", Handle: "alice",
			Roles: []common.Role{"user"}, LandingPage: common.Collection,
		}, nil
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
		LandingPage string `json:"landing_page"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.LandingPage != "collection" {
		t.Fatalf("landing_page: %q, want collection", got.LandingPage)
	}
}

// captureUsers embeds the stub so Get and Delete forward unchanged,
// while Update additionally exposes the raw body reaching the user
// service (mirrors captureCollection's pass-through capture).
type captureUsers struct {
	*stubUsers
	onUpdate func(body []byte)
}

func (c *captureUsers) Update(ctx context.Context, id, bearer string, body []byte) (userclient.Result, error) {
	c.onUpdate(body)
	return c.stubUsers.Update(ctx, id, bearer, body)
}

// TestUnitUpdateMe_RelaysPreferredCurrency pins that the PATCH body
// reaches the user service verbatim and the answer projects the field.
func TestUnitUpdateMe_RelaysPreferredCurrency(t *testing.T) {
	uid := uuid.New()
	userJSON := []byte(`{"id":"` + uid.String() + `","email":"alice@example.test","handle":"alice","preferred_currency":"JPY","roles":["user"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`)
	users := &stubUsers{update: func(context.Context, string, string, []byte) (userclient.Result, error) {
		return userclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: userJSON}, nil
	}}
	var gotBody []byte
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.users = &captureUsers{stubUsers: users, onUpdate: func(body []byte) { gotBody = body }}
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

// TestUnitUpdateMe_RelaysLandingPage pins that a PATCH carrying
// landing_page reaches the user client verbatim in the request body,
// and the answer's landing_page projects onto the composed Me.
func TestUnitUpdateMe_RelaysLandingPage(t *testing.T) {
	uid := uuid.New()
	userJSON := []byte(`{"id":"` + uid.String() + `","email":"alice@example.test","handle":"alice","landing_page":"collection","roles":["user"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`)
	users := &stubUsers{update: func(context.Context, string, string, []byte) (userclient.Result, error) {
		return userclient.Result{Status: http.StatusOK, ContentType: "application/json", Body: userJSON}, nil
	}}
	var gotBody []byte
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.users = &captureUsers{stubUsers: users, onUpdate: func(body []byte) { gotBody = body }}
	access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
	r := httptest.NewRequest(http.MethodPatch, "/api/me", strings.NewReader(`{"landing_page":"collection"}`))
	r.AddCookie(sealedCookie(t, h, access, "r1"))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)

	if !strings.Contains(string(gotBody), `"landing_page":"collection"`) {
		t.Fatalf("relayed body = %s, want it to contain landing_page collection", gotBody)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		LandingPage string `json:"landing_page"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.LandingPage != "collection" {
		t.Fatalf("landing_page: %q, want collection", got.LandingPage)
	}
}

func TestGetMyIdentities(t *testing.T) {
	t.Run("200_in_list_order", func(t *testing.T) {
		uid := uuid.New()
		id1, id2 := uuid.New(), uuid.New()
		emailA := "alice@example.test"
		t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		t2 := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
		h := newTestHandlers(t, newStubCache(), &stubAuth{
			listIdentities: func(_ context.Context, userID, _ string) ([]common.Identity, error) {
				if userID != uid.String() {
					t.Errorf("userID = %q", userID)
				}
				return []common.Identity{
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
		h := newTestHandlers(t, newStubCache(), &stubAuth{
			listIdentities: func(context.Context, string, string) ([]common.Identity, error) {
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
			h := newTestHandlers(t, newStubCache(), &stubAuth{
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
		h := newTestHandlers(t, newStubCache(), &stubAuth{
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
		h := newTestHandlers(t, newStubCache(), &stubAuth{
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
		h := newTestHandlers(t, newStubCache(), &stubAuth{
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
		h := newTestHandlers(t, newStubCache(), &stubAuth{
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
		soc := &stubSocialFull{purgeUserData: func(context.Context, string) (socialclient.Result, error) {
			record("social.PurgeUserData")
			return socialclient.Result{Status: http.StatusNoContent}, nil
		}}
		h := newTestHandlers(t, fc, &stubAuth{
			deleteUserAuth: func(context.Context, string, string) error {
				record("auth.DeleteUserAuth")
				return nil
			},
		})
		h.collection = col
		h.social = soc
		h.users = &stubUsers{delete: func(context.Context, string, string) error { record("users.Delete"); return nil }}
		access := mintAccess(t, uid.String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodDelete, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
		}
		if want := []string{"collection.PurgeUserData", "social.PurgeUserData", "auth.DeleteUserAuth", "users.Delete"}; !slices.Equal(order, want) {
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
				h := newTestHandlers(t, newStubCache(), &stubAuth{
					deleteUserAuth: func(context.Context, string, string) error {
						record("auth.DeleteUserAuth")
						return nil
					},
				})
				h.collection = col
				h.users = &stubUsers{delete: func(context.Context, string, string) error { record("users.Delete"); return nil }}
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

	t.Run("social_purge_failure_stops_before_auth_or_user", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			res  socialclient.Result
			err  error
		}{
			{"non_204_result", socialclient.Result{Status: http.StatusInternalServerError}, nil},
			{"transport_error", socialclient.Result{}, errors.New("social down")},
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
					return collectionclient.Result{Status: http.StatusNoContent}, nil
				}}
				soc := &stubSocialFull{purgeUserData: func(context.Context, string) (socialclient.Result, error) {
					record("social.PurgeUserData")
					return tc.res, tc.err
				}}
				h := newTestHandlers(t, newStubCache(), &stubAuth{
					deleteUserAuth: func(context.Context, string, string) error {
						record("auth.DeleteUserAuth")
						return nil
					},
				})
				h.collection = col
				h.social = soc
				h.users = &stubUsers{delete: func(context.Context, string, string) error { record("users.Delete"); return nil }}
				access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
				r := httptest.NewRequest(http.MethodDelete, "/api/me", nil)
				r.AddCookie(sealedCookie(t, h, access, "r1"))
				rec := httptest.NewRecorder()
				newRouterFor(t, h).ServeHTTP(rec, r)

				if rec.Code != http.StatusBadGateway {
					t.Fatalf("code = %d", rec.Code)
				}
				var p struct {
					Code   string `json:"code"`
					Detail string `json:"detail"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
					t.Fatalf("problem body: %v (%s)", err, rec.Body.String())
				}
				if p.Code != "upstream_error" || p.Detail != "social purge failed; retry" {
					t.Fatalf("problem = %+v, want code=upstream_error detail=%q", p, "social purge failed; retry")
				}
				if want := []string{"collection.PurgeUserData", "social.PurgeUserData"}; !slices.Equal(order, want) {
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
		soc := &stubSocialFull{purgeUserData: func(context.Context, string) (socialclient.Result, error) {
			record("social.PurgeUserData")
			return socialclient.Result{Status: http.StatusNoContent}, nil
		}}
		h := newTestHandlers(t, newStubCache(), &stubAuth{
			deleteUserAuth: func(context.Context, string, string) error {
				record("auth.DeleteUserAuth")
				return errors.New("auth down")
			},
		})
		h.collection = col
		h.social = soc
		h.users = &stubUsers{delete: func(context.Context, string, string) error { record("users.Delete"); return nil }}
		access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
		r := httptest.NewRequest(http.MethodDelete, "/api/me", nil)
		r.AddCookie(sealedCookie(t, h, access, "r1"))
		rec := httptest.NewRecorder()
		newRouterFor(t, h).ServeHTTP(rec, r)

		if rec.Code != http.StatusBadGateway {
			t.Fatalf("code = %d", rec.Code)
		}
		if want := []string{"collection.PurgeUserData", "social.PurgeUserData", "auth.DeleteUserAuth"}; !slices.Equal(order, want) {
			t.Fatalf("order = %v, want %v (user delete must not run)", order, want)
		}
		if clearedCookie(rec) {
			t.Fatal("a mid-failure must keep the session intact")
		}
	})
}
