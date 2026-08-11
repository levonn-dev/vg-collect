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

	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/authapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/enrichapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
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
// call order. The shared-page surface (added for the social pages) follows
// the newer function-field, nil-panics convention instead.
type stubUsersFull struct {
	user     userapi.User
	err      error
	result   userclient.Result
	onDelete func()

	sharedProfile    func(ctx context.Context, bearer, handle string) (userapi.ProfileCard, error)
	sharedCardsByIDs func(ctx context.Context, bearer string, ids []uuid.UUID) ([]userapi.ProfileCard, error)
	searchProfiles   func(ctx context.Context, bearer, q string) (userclient.Result, error)
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

func (f *stubUsersFull) SharedProfile(ctx context.Context, bearer, handle string) (userapi.ProfileCard, error) {
	if f.sharedProfile == nil {
		panic("unexpected users.SharedProfile")
	}
	return f.sharedProfile(ctx, bearer, handle)
}

func (f *stubUsersFull) SharedCardsByIDs(ctx context.Context, bearer string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
	if f.sharedCardsByIDs == nil {
		panic("unexpected users.SharedCardsByIDs")
	}
	return f.sharedCardsByIDs(ctx, bearer, ids)
}

func (f *stubUsersFull) SearchProfiles(ctx context.Context, bearer, q string) (userclient.Result, error) {
	if f.searchProfiles == nil {
		panic("unexpected users.SearchProfiles")
	}
	return f.searchProfiles(ctx, bearer, q)
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

	normalizeCommunityRegions func(ctx context.Context, bearer string) (enrichmentclient.Result, error)
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

func (s *stubEnrichment) NormalizeCommunityRegions(ctx context.Context, bearer string) (enrichmentclient.Result, error) {
	if s.normalizeCommunityRegions == nil {
		panic("unexpected NormalizeCommunityRegions")
	}
	return s.normalizeCommunityRegions(ctx, bearer)
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

// TestUnitSharedProfilesByIds_RelaysEnvelopeAndForwardsIds proves the
// batch profile-card hydration marshals the user service's typed
// cards into the {profiles: [...]} envelope the frontend expects
// (unlike the pass-through relays above, the upstream answer is typed,
// not a raw body relay) and forwards the exact ids the browser asked
// for; no session is 401.
func TestUnitSharedProfilesByIds_RelaysEnvelopeAndForwardsIds(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()
	var gotIDs []uuid.UUID
	users := &stubUsersFull{sharedCardsByIDs: func(_ context.Context, _ string, ids []uuid.UUID) ([]userapi.ProfileCard, error) {
		gotIDs = ids
		return []userapi.ProfileCard{
			{UserId: id1, Handle: "alice", ProfileVisibility: "listed"},
			{UserId: id2, Handle: "bob", ProfileVisibility: "private"},
		}, nil
	}}
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
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

// TestUnitHandlers_OwnSessionGuards calls handlers directly, without
// the Authenticate middleware in front, to prove each one enforces its
// own session check: the in-handler guard is defense in depth and must
// hold even if a handler is ever wired up without the middleware.
func TestUnitHandlers_OwnSessionGuards(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuthFull{})
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
