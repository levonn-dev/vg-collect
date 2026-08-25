package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/levonn-dev/vgkeep/libs/go/contract/userapi"
)

func testLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func newRouterFor(t *testing.T, h *Handlers) http.Handler {
	t.Helper()
	router, err := NewRouter(h, nil, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	return router
}

// testEnv bundles the session cookie and its sealed access token, so a
// pass-through test can drive a request and assert the exact bearer that must ride it.
type testEnv struct {
	cookie             *http.Cookie
	sessionAccessToken string
}

// newTestHandlersWithEnrichment builds Handlers wired to enrich with a fresh, never-refreshing session.
func newTestHandlersWithEnrichment(t *testing.T, enrich *stubEnrichment) (*Handlers, *testEnv) {
	t.Helper()
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.enrichment = enrich
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	return h, &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}
}

func doAuthed(t *testing.T, h *Handlers, env *testEnv, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.AddCookie(env.cookie)
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, r)
	return rec
}

// doUnauthed drives method/path with no session cookie: the guard must answer before any handler runs.
func doUnauthed(t *testing.T, h *Handlers, env *testEnv, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	_ = env
	rec := httptest.NewRecorder()
	newRouterFor(t, h).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// doAuthedBody mirrors doAuthed for a mutating request: env's sealed cookie,
// an allowed Origin (CheckOrigin runs first), and body as the JSON request body.
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

func TestHealthAndStaticPlaceholder(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
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

// TestUnitSharedProfilesByIds_RelaysEnvelopeAndForwardsIds marshals typed
// cards into {profiles: [...]} (unlike raw-body relays) and forwards the exact ids; no session is 401.
func TestUnitSharedProfilesByIds_RelaysEnvelopeAndForwardsIds(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()
	var gotIDs []uuid.UUID
	users := &stubUsers{sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
		gotIDs = ids
		return []userapi.ProfileCard{
			{UserId: id1, Handle: "alice", ProfileVisibility: "listed"},
			{UserId: id2, Handle: "bob", ProfileVisibility: "private"},
		}, nil
	}}
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	h.users = users
	access := mintAccess(t, uuid.New().String(), "j1", time.Now().Add(5*time.Minute))
	env := &testEnv{cookie: sealedCookie(t, h, access, "r1"), sessionAccessToken: access}

	rec := doAuthed(t, h, env, http.MethodGet, "/api/shared/profiles/by-ids?ids="+id1.String()+"&ids="+id2.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Profiles []struct {
			UserID            string `json:"user_id"`
			Handle            string `json:"handle"`
			ProfileVisibility string `json:"profile_visibility"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	if len(got.Profiles) != 2 || got.Profiles[0].Handle != "alice" || got.Profiles[1].Handle != "bob" {
		t.Fatalf("profiles envelope: %+v", got.Profiles)
	}
	if len(gotIDs) != 2 || gotIDs[0] != id1 || gotIDs[1] != id2 {
		t.Fatalf("ids passthrough: %v", gotIDs)
	}

	rec = doUnauthed(t, h, env, http.MethodGet, "/api/shared/profiles/by-ids?ids="+id1.String())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: %d", rec.Code)
	}
}

func TestUnitAdminRoutes_NoSession401(t *testing.T) {
	h, env := newTestHandlersWithEnrichment(t, &stubEnrichment{})
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/admin/products/unmatched"},
		{http.MethodGet, "/api/admin/products/community"},
		{http.MethodPut, "/api/admin/products/" + uuid.NewString() + "/pricecharting"},
		{http.MethodPost, "/api/admin/refresh"},
		{http.MethodPost, "/api/admin/rematch"},
	} {
		rec := doUnauthed(t, h, env, tc.method, tc.path)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: %d", tc.method, tc.path, rec.Code)
		}
	}
}

// TestUnitHandlers_OwnSessionGuards calls handlers directly (no Authenticate
// middleware) to prove each enforces its own session check as defense in depth.
func TestUnitHandlers_OwnSessionGuards(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	id := uuid.New()
	calls := map[string]func(w http.ResponseWriter, r *http.Request){
		"list_platforms":      func(w http.ResponseWriter, r *http.Request) { h.ListPlatforms(w, r) },
		"get_product":         func(w http.ResponseWriter, r *http.Request) { h.GetProduct(w, r, id) },
		"trigger_refresh":     func(w http.ResponseWriter, r *http.Request) { h.TriggerRefresh(w, r) },
		"trigger_rematch":     func(w http.ResponseWriter, r *http.Request) { h.TriggerRematch(w, r) },
		"resnapshot":          func(w http.ResponseWriter, r *http.Request) { h.Resnapshot(w, r) },
		"create_submission":   func(w http.ResponseWriter, r *http.Request) { h.CreateSubmission(w, r, id) },
		"get_submission":      func(w http.ResponseWriter, r *http.Request) { h.GetSubmission(w, r, id) },
		"ack_submission":      func(w http.ResponseWriter, r *http.Request) { h.AckSubmissionResolution(w, r, id) },
		"submit_verdict":      func(w http.ResponseWriter, r *http.Request) { h.SubmitVerdict(w, r, id) },
		"promote_product":     func(w http.ResponseWriter, r *http.Request) { h.PromoteProduct(w, r, id) },
		"ack_region_mismatch": func(w http.ResponseWriter, r *http.Request) { h.AckEntryRegionMismatch(w, r, id) },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			call(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("handler must enforce its own session guard, code = %d", rec.Code)
			}
		})
	}
}
