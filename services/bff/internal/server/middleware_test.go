package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"

	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/services/bff/internal/authclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/cache"
	"github.com/levonn-dev/vgkeep/services/bff/internal/collectionclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/enrichmentclient"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/authapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/gen/userapi"
	"github.com/levonn-dev/vgkeep/services/bff/internal/session"
	"github.com/levonn-dev/vgkeep/services/bff/internal/userclient"
)

const testCookieKey = "dmctY29sbGVjdC1kZXYtY29va2llLWtleS0wMDAwMDE="

type stubCache struct {
	mu       sync.Mutex
	deny     map[string]bool
	locks    map[string]string
	results  map[string]string
	me       map[string][]byte
	recs     map[string][]byte
	denyAdds [][]string
	err      error // returned by every method when set
}

func newStubCache() *stubCache {
	return &stubCache{deny: map[string]bool{}, locks: map[string]string{},
		results: map[string]string{}, me: map[string][]byte{}, recs: map[string][]byte{}}
}

func (f *stubCache) DenylistAdd(_ context.Context, jtis []string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.denyAdds = append(f.denyAdds, jtis)
	for _, j := range jtis {
		f.deny[j] = true
	}
	return nil
}

func (f *stubCache) DenylistHas(_ context.Context, jti string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	return f.deny[jti], nil
}

func (f *stubCache) AcquireRefreshLock(_ context.Context, key, holder string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	if _, held := f.locks[key]; held {
		return false, nil
	}
	f.locks[key] = holder
	return true, nil
}

func (f *stubCache) ReleaseRefreshLock(_ context.Context, key, holder string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.locks[key] == holder {
		delete(f.locks, key)
	}
	return nil
}

func (f *stubCache) PutRefreshResult(_ context.Context, key, sealed string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.results[key] = sealed
	return nil
}

func (f *stubCache) GetRefreshResult(_ context.Context, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return "", f.err
	}
	return f.results[key], nil
}

func (f *stubCache) GetMe(_ context.Context, sub string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.me[sub], nil
}

func (f *stubCache) PutMe(_ context.Context, sub string, body []byte, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.me[sub] = body
	return nil
}

func (f *stubCache) GetRecs(_ context.Context, sub string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.recs[sub], nil
}

func (f *stubCache) PutRecs(_ context.Context, sub string, body []byte, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.recs[sub] = body
	return nil
}

func (f *stubCache) InvalidateRecs(_ context.Context, sub string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	delete(f.recs, sub)
	return nil
}

func (f *stubCache) InvalidateMe(_ context.Context, sub string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	delete(f.me, sub)
	return nil
}

// stubAuth panics on everything; tests override what they use.
type stubAuth struct {
	refresh func(ctx context.Context, refreshToken string) (authclient.TokenPair, error)
}

func (s *stubAuth) Start(context.Context, string) (string, error) { panic("unexpected Start") }
func (s *stubAuth) Callback(context.Context, string, string) (authclient.TokenPair, error) {
	panic("unexpected Callback")
}
func (s *stubAuth) DevToken(context.Context, string) (authclient.TokenPair, error) {
	panic("unexpected DevToken")
}
func (s *stubAuth) Revoke(context.Context, string) error        { panic("unexpected Revoke") }
func (s *stubAuth) Providers(context.Context) ([]string, error) { panic("unexpected Providers") }
func (s *stubAuth) Refresh(ctx context.Context, rt string) (authclient.TokenPair, error) {
	if s.refresh == nil {
		panic("unexpected Refresh")
	}
	return s.refresh(ctx, rt)
}
func (s *stubAuth) LinkStart(context.Context, string, string) (string, error) {
	panic("unexpected LinkStart")
}
func (s *stubAuth) DevLink(context.Context, string, string) (authclient.TokenPair, error) {
	panic("unexpected DevLink")
}
func (s *stubAuth) ListIdentities(context.Context, string, string) ([]authapi.Identity, error) {
	panic("unexpected ListIdentities")
}
func (s *stubAuth) DeleteIdentity(context.Context, uuid.UUID, string) error {
	panic("unexpected DeleteIdentity")
}
func (s *stubAuth) DeleteUserAuth(context.Context, string, string) error {
	panic("unexpected DeleteUserAuth")
}

type stubUsers struct{}

func (stubUsers) Get(context.Context, string, string) (userapi.User, error) {
	panic("unexpected users.Get")
}

func (stubUsers) Update(context.Context, string, string, []byte) (userclient.Result, error) {
	panic("unexpected users.Update")
}

func (stubUsers) Delete(context.Context, string, string) error {
	panic("unexpected users.Delete")
}

func (stubUsers) SharedProfile(context.Context, string, string) (userapi.ProfileCard, error) {
	panic("unexpected users.SharedProfile")
}

func (stubUsers) SharedCardsByIDs(context.Context, string, []uuid.UUID) ([]userapi.ProfileCard, error) {
	panic("unexpected users.SharedCardsByIDs")
}

func (stubUsers) SearchProfiles(context.Context, string, string) (userclient.Result, error) {
	panic("unexpected users.SearchProfiles")
}

// ---- helpers ----

func mintAccess(t *testing.T, sub, jti string, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": sub, "jti": jti, "exp": exp.Unix(),
	})
	s, err := tok.SignedString([]byte("test"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func newTestHandlers(t *testing.T, c SessionCache, a AuthAPI) *Handlers {
	t.Helper()
	codec, err := session.NewCodec(testCookieKey, true)
	if err != nil {
		t.Fatal(err)
	}
	h := New(codec, c, a, stubUsers{}, &stubEnrichment{}, &stubCollection{}, &stubSocialFull{}, Options{
		AccessTokenTTL: 5 * time.Minute,
		RefreshWindow:  30 * time.Second,
		MeCacheTTL:     45 * time.Second,
		PublicOrigins:  []string{"http://localhost:8090", "http://localhost:5173"},
		Logger:         slog.New(slog.DiscardHandler),
	})
	h.pollInterval = 5 * time.Millisecond
	h.pollBudget = 250 * time.Millisecond
	return h
}

func sealedCookie(t *testing.T, h *Handlers, access, refresh string) *http.Cookie {
	t.Helper()
	sealed, err := h.codec.Seal(session.Session{AccessToken: access, RefreshToken: refresh})
	if err != nil {
		t.Fatal(err)
	}
	return h.codec.Cookie(sealed, 3600)
}

// echoNext records that it ran and what session it saw.
func echoNext(t *testing.T, sawAccess *string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess, _, ok := session.FromContext(r.Context()); ok {
			*sawAccess = sess.AccessToken
		}
		w.WriteHeader(http.StatusOK)
	})
}

func doAuth(h *Handlers, r *http.Request, next http.Handler) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.Authenticate(next).ServeHTTP(rec, r)
	return rec
}

func clearedCookie(rec *httptest.ResponseRecorder) bool {
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName && c.MaxAge < 0 {
			return true
		}
	}
	return false
}

func mustResultJSON(t *testing.T, sealed string, maxAge int) string {
	t.Helper()
	b, err := json.Marshal(refreshResult{Cookie: sealed, MaxAge: maxAge})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// stubAuthService models the auth service's refresh rotation and reuse
// detection over real HTTP. It is single-use per refresh token: a
// not-yet-consumed token rotates to a fresh pair (new access jti), and
// replaying a consumed token returns refresh_reused carrying the live
// jti for that chain (exactly what the real auth service revokes).
type stubAuthService struct {
	t   *testing.T
	srv *httptest.Server

	mu sync.Mutex
	// consumed marks refresh tokens already spent by a rotation.
	consumed map[string]bool
	// liveJTI maps a refresh token to the access jti minted alongside it
	// (the still-live jti the reuse response must report for that chain).
	liveJTI map[string]string
	// chainSub maps a refresh token to the subject its chain belongs to,
	// so a rotation mints the successor for the same user (the bff sends
	// only the refresh token to /token/refresh).
	chainSub map[string]string
	// next numbers freshly minted refresh tokens (refresh-2, refresh-3...).
	next int

	// refreshCalls counts every /token/refresh request served, for the
	// singleflight assertion.
	refreshCalls atomic.Int32
	// refreshDelay widens the concurrency window while the lock is held.
	refreshDelay time.Duration
}

func newStubAuthService(t *testing.T) *stubAuthService {
	t.Helper()
	f := &stubAuthService{
		t:        t,
		consumed: map[string]bool{},
		liveJTI:  map[string]string{},
		chainSub: map[string]string{},
		next:     1,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token/refresh", f.refresh)
	mux.HandleFunc("POST /token/revoke", f.revoke)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// seedChain registers a chain's starting refresh token with the subject
// and live access jti the test sealed into the cookie, so the first
// rotation mints a successor for the same user and a reuse of that token
// reports the right live jti.
func (f *stubAuthService) seedChain(refreshToken, sub, accessJTI string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chainSub[refreshToken] = sub
	f.liveJTI[refreshToken] = accessJTI
}

// markConsumed pre-marks a refresh token as already spent, so the first
// refresh of it returns refresh_reused (the stolen/replayed-token case).
func (f *stubAuthService) markConsumed(refreshToken, sub, liveJTI string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consumed[refreshToken] = true
	f.chainSub[refreshToken] = sub
	f.liveJTI[refreshToken] = liveJTI
}

func (f *stubAuthService) refresh(w http.ResponseWriter, r *http.Request) {
	f.refreshCalls.Add(1)
	if f.refreshDelay > 0 {
		time.Sleep(f.refreshDelay)
	}
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("refresh: decode body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	rt := req.RefreshToken
	if f.consumed[rt] {
		// Reuse: the chain is dead. Report the live access jti so the bff
		// denylists it (the real auth service revokes the family here).
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "about:blank", "title": "Unauthorized", "status": 401,
			"code": "refresh_reused", "revoke_jtis": []string{f.liveJTI[rt]},
		})
		return
	}

	sub := f.chainSub[rt]
	if sub == "" {
		sub = "u1"
	}
	f.consumed[rt] = true
	f.next++
	rt2 := "refresh-" + itoa(f.next)
	newJTI := "jti-" + itoa(f.next)
	access := mintAccess(f.t, sub, newJTI, time.Now().Add(5*time.Minute))
	// Carry the chain forward: the successor token belongs to the same
	// user and its live jti is the one just minted.
	f.chainSub[rt2] = sub
	f.liveJTI[rt2] = newJTI

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": access, "token_type": "Bearer", "expires_in": 300,
		"refresh_token": rt2, "refresh_expires_in": 2591999,
	})
}

func (f *stubAuthService) revoke(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// itoa avoids importing strconv just for small positive integers.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// stubUserService answers GET /users/{id} with a canned profile. The
// test can switch it to error (500) or 404 on demand to drive the
// cache/compose paths.
type stubUserService struct {
	srv *httptest.Server

	mu     sync.Mutex
	id     string
	email  string
	handle string
	mode   userMode
}

type userMode int

const (
	userOK userMode = iota
	userError
	userGone
)

func newStubUserService(t *testing.T, id, email, handle string) *stubUserService {
	t.Helper()
	f := &stubUserService{id: id, email: email, handle: handle, mode: userOK}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", f.get)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stubUserService) setMode(m userMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = m
}

func (f *stubUserService) get(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.mode {
	case userError:
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "about:blank", "title": "Internal Server Error", "status": 500,
		})
		return
	case userGone:
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "about:blank", "title": "Not Found", "status": 404, "code": "user_not_found",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": f.id, "email": f.email, "handle": f.handle,
		"roles": []string{"user"}, "created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
	})
}

// stubEnrichmentService answers GET /search with a canned SearchResults
// body, recording every call's Authorization header and a running
// count: the never-cached-at-the-bff proof needs an exact hit count.
type stubEnrichmentService struct {
	srv *httptest.Server

	mu         sync.Mutex
	calls      int
	gotAuth    []string
	scoreCalls int
}

func newStubEnrichmentService(t *testing.T) *stubEnrichmentService {
	t.Helper()
	f := &stubEnrichmentService{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /search", f.search)
	mux.HandleFunc("POST /recommendations:score", f.score)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stubEnrichmentService) search(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls++
	f.gotAuth = append(f.gotAuth, r.Header.Get("Authorization"))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"degraded":false,"results":[{"type":"game","name":"Chrono Trigger"}]}`))
}

func (f *stubEnrichmentService) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// score answers the recommendation scorer the composed route calls;
// scoreCalls is the cache-lifecycle proof's hit counter.
func (f *stubEnrichmentService) score(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.scoreCalls++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"degraded":false,"recommendations":[{"igdb_game_id":9,"name":"Alundra","genres":["RPG"],"score":4.2}]}`))
}

func (f *stubEnrichmentService) scoreCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.scoreCalls
}

// stubCollectionService answers GET /entries with a canned EntryList
// body, recording every call's Authorization header and a running
// count: the never-cached-at-the-bff proof needs an exact hit count.
type stubCollectionService struct {
	srv *httptest.Server

	mu               sync.Mutex
	calls            int
	gotAuth          []string
	createCalls      int
	valueHistoryHits int
}

func newStubCollectionService(t *testing.T) *stubCollectionService {
	t.Helper()
	f := &stubCollectionService{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /entries", f.entries)
	mux.HandleFunc("GET /library/summary", f.librarySummary)
	mux.HandleFunc("POST /entries", f.createEntry)
	mux.HandleFunc("GET /dashboard/value-history", f.valueHistory)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stubCollectionService) entries(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls++
	f.gotAuth = append(f.gotAuth, r.Header.Get("Authorization"))
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"pricing_available":true,"total_count":0,"entries":[]}`))
}

func (f *stubCollectionService) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// valueHistory answers the value-history pass-through the
// never-cached-at-the-bff proof drives; valueHistoryHits records how
// many times the collection service was actually reached.
func (f *stubCollectionService) valueHistory(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.valueHistoryHits++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"available":true,"points":[]}`))
}

func (f *stubCollectionService) valueHistoryHitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.valueHistoryHits
}

// librarySummary answers the recommendations composition's library
// read with a fixed one-game library.
func (f *stubCollectionService) librarySummary(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"library":[{"igdb_game_id":9,"rating":8}]}`))
}

// createEntry answers the entry-creation mutation the invalidation
// test drives; createCalls records how many times it ran.
func (f *stubCollectionService) createEntry(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.createCalls++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{}`))
}

func (f *stubCollectionService) createCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createCalls
}

// stubOtlpCollector answers POST /v1/traces with a canned 200, recording
// a running hit count: the never-cached-at-the-bff proof needs an exact
// hit count, just like the other pass-throughs.
type stubOtlpCollector struct {
	srv *httptest.Server

	mu   sync.Mutex
	hits int
}

func newStubOtlpCollector(t *testing.T) *stubOtlpCollector {
	t.Helper()
	f := &stubOtlpCollector{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/traces", f.traces)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *stubOtlpCollector) traces(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.hits++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{}`))
}

func (f *stubOtlpCollector) hitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

type stack struct {
	baseURL    string
	codec      *session.Codec
	auth       *stubAuthService
	users      *stubUserService
	enrich     *stubEnrichmentService
	collection *stubCollectionService
	otlp       *stubOtlpCollector
	client     *http.Client
}

// newStack wires the whole bff vertical: a Valkey container behind
// cache.New, the authclient/userclient against httptest stubs, and the
// codec + Handlers + router on an httptest server. Skips on -short.
func newStack(t *testing.T) *stack {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()

	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })
	url, err := vk.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client, err := valkeykit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	c := cache.New(client)

	fa := newStubAuthService(t)
	authClient, err := authclient.New(fa.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	fu := newStubUserService(t, "", "", "")
	userClient, err := userclient.New(fu.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	fe := newStubEnrichmentService(t)
	enrichClient, err := enrichmentclient.New(fe.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	fcol := newStubCollectionService(t)
	collClient, err := collectionclient.New(fcol.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	fo := newStubOtlpCollector(t)

	codec, err := session.NewCodec(testCookieKey, true)
	if err != nil {
		t.Fatal(err)
	}
	// No stubSocialService double exists: none of this stack's tests
	// exercise a social route, so a nil-panic stub (same convention as
	// every other unused surface here) is the correct no-op.
	h := New(codec, c, authClient, userClient, enrichClient, collClient, &stubSocialFull{}, Options{
		AccessTokenTTL: 5 * time.Minute,
		RefreshWindow:  30 * time.Second,
		MeCacheTTL:     45 * time.Second,
		PublicOrigins:  []string{"http://localhost:8090", "http://localhost:5173"},
		OTLPProxyURL:   fo.srv.URL,
		Logger:         slog.New(slog.DiscardHandler),
	})
	// Keep adopt-polls fast, as the unit tests do.
	h.pollInterval = 5 * time.Millisecond
	h.pollBudget = 250 * time.Millisecond

	srv := httptest.NewServer(NewRouter(h, nil, slog.New(slog.DiscardHandler)))
	t.Cleanup(srv.Close)

	return &stack{
		baseURL:    srv.URL,
		codec:      codec,
		auth:       fa,
		users:      fu,
		enrich:     fe,
		collection: fcol,
		otlp:       fo,
		client:     srv.Client(),
	}
}

// cookieFor seals (access, refresh) into the session cookie. The cookie
// is attached to requests explicitly (see getMe): a Go http client will
// not send a __Host--prefixed cookie over http via a jar, but the bff
// reads r.Cookie(session.CookieName), so an explicitly-added cookie of
// that name is honored regardless of prefix rules.
func (s *stack) cookieFor(t *testing.T, access, refresh string) *http.Cookie {
	t.Helper()
	sealed, err := s.codec.Seal(session.Session{AccessToken: access, RefreshToken: refresh})
	if err != nil {
		t.Fatal(err)
	}
	return s.codec.Cookie(sealed, 3600)
}

// meResult is one /api/me round-trip's observable outcome.
type meResult struct {
	status  int
	body    []byte
	cleared bool
}

// getMe issues GET /api/me through the real server carrying cookie.
func (s *stack) getMe(t *testing.T, cookie *http.Cookie) meResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return meResult{status: resp.StatusCode, body: body, cleared: clearsCookie(resp)}
}

// clearsCookie reports whether the response expired the session cookie.
func clearsCookie(resp *http.Response) bool {
	for _, c := range resp.Cookies() {
		if c.Name == session.CookieName && c.MaxAge < 0 {
			return true
		}
	}
	return false
}

// searchResult is one /api/search round-trip's observable outcome.
type searchResult struct {
	status int
	body   []byte
}

// search issues GET /api/search?type=&q= through the real server
// carrying cookie.
func (s *stack) search(t *testing.T, cookie *http.Cookie, typ, q string) searchResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/search?type="+typ+"&q="+q, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return searchResult{status: resp.StatusCode, body: body}
}

// entriesResult is one /api/entries round-trip's observable outcome.
type entriesResult struct {
	status int
	body   []byte
}

// entries issues GET /api/entries through the real server carrying
// cookie.
func (s *stack) entries(t *testing.T, cookie *http.Cookie) entriesResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/entries", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return entriesResult{status: resp.StatusCode, body: body}
}

// valueHistoryResult is one /api/dashboard/value-history round-trip's
// observable outcome.
type valueHistoryResult struct {
	status int
	body   []byte
}

// valueHistory issues GET /api/dashboard/value-history through the
// real server carrying cookie.
func (s *stack) valueHistory(t *testing.T, cookie *http.Cookie) valueHistoryResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/dashboard/value-history", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return valueHistoryResult{status: resp.StatusCode, body: body}
}

// recommendationsResult is one /api/recommendations round-trip's
// observable outcome.
type recommendationsResult struct {
	status int
	body   []byte
}

// recommendations issues GET /api/recommendations through the real
// server carrying cookie.
func (s *stack) recommendations(t *testing.T, cookie *http.Cookie) recommendationsResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.baseURL+"/api/recommendations", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return recommendationsResult{status: resp.StatusCode, body: body}
}

// createEntry issues POST /api/entries (with the Origin header the
// CheckOrigin middleware requires of a mutating request) through the
// real server carrying cookie.
func (s *stack) createEntry(t *testing.T, cookie *http.Cookie) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/api/entries", strings.NewReader(`{"product_id":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://localhost:8090")
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// otlpTraces issues POST /api/otlp/v1/traces with a JSON OTLP payload
// through the real server; cookie nil drives the cookieless case.
func (s *stack) otlpTraces(t *testing.T, cookie *http.Cookie) int {
	t.Helper()
	const body = `{"resourceSpans":[{"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"test"}}]}}]}`
	req, err := http.NewRequest(http.MethodPost, s.baseURL+"/api/otlp/v1/traces", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// ---- tests ----

func TestAllowlistBypassesAuth(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	for _, path := range []string{
		"/api/auth/login", "/api/auth/callback", "/api/auth/logout",
		"/api/auth/providers", "/healthz", "/readyz", "/", "/login", "/assets/app.js",
	} {
		ran := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true })
		rec := doAuth(h, httptest.NewRequest(http.MethodGet, path, nil), next)
		if !ran || rec.Code != http.StatusOK {
			t.Errorf("%s: ran=%v code=%d (allowlist must bypass auth)", path, ran, rec.Code)
		}
	}
}

func TestProtectedRequiresSession(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})

	rec := doAuth(h, httptest.NewRequest(http.MethodGet, "/api/me", nil), echoNext(t, new(string)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: code = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(&http.Cookie{Name: session.CookieName, Value: "garbage"}) //nolint:gosec // G124: test cookie; no browser enforces Secure in unit tests
	rec = doAuth(h, r, echoNext(t, new(string)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("garbage cookie: code = %d", rec.Code)
	}
	if !clearedCookie(rec) {
		t.Fatal("garbage cookie should be cleared")
	}

	// /api/auth/link is a navigation like /api/auth/login, but unlike it
	// the link target is session-guarded: linking acts on an existing
	// account, so it must never be allowlisted.
	rec = doAuth(h, httptest.NewRequest(http.MethodGet, "/api/auth/link?provider=dev", nil), echoNext(t, new(string)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("/api/auth/link without a session: code = %d, want 401 (it must not be allowlisted)", rec.Code)
	}
}

func TestFreshTokenPassesThrough(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{}) // Refresh would panic
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	var saw string
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "refresh-1"))
	rec := doAuth(h, r, echoNext(t, &saw))
	if rec.Code != http.StatusOK || saw != access {
		t.Fatalf("code=%d sawAccess=%v", rec.Code, saw == access)
	}
}

func TestDenylistedRejected(t *testing.T) {
	fc := newStubCache()
	fc.deny["j1"] = true
	h := newTestHandlers(t, fc, &stubAuth{})
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "refresh-1"))
	rec := doAuth(h, r, echoNext(t, new(string)))
	if rec.Code != http.StatusUnauthorized || !clearedCookie(rec) {
		t.Fatalf("code=%d cleared=%v", rec.Code, clearedCookie(rec))
	}
}

func TestDenylistFailsOpen(t *testing.T) {
	fc := newStubCache()
	fc.err = errors.New("valkey down")
	h := newTestHandlers(t, fc, &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
		t.Fatal("refresh must not run for a fresh token")
		return authclient.TokenPair{}, nil
	}})
	access := mintAccess(t, "u1", "j1", time.Now().Add(5*time.Minute))
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "refresh-1"))
	rec := doAuth(h, r, echoNext(t, new(string)))
	if rec.Code != http.StatusOK {
		t.Fatalf("denylist outage must fail open, code = %d", rec.Code)
	}
}

func TestExpiringTokenRefreshes(t *testing.T) {
	fc := newStubCache()
	newAccess := mintAccess(t, "u1", "j2", time.Now().Add(5*time.Minute))
	h := newTestHandlers(t, fc, &stubAuth{refresh: func(_ context.Context, rt string) (authclient.TokenPair, error) {
		if rt != "refresh-1" {
			t.Errorf("refreshed with %q", rt)
		}
		return authclient.TokenPair{AccessToken: newAccess, RefreshToken: "refresh-2",
			ExpiresIn: 300, RefreshExpiresIn: 1000}, nil
	}})
	oldAccess := mintAccess(t, "u1", "j1", time.Now().Add(10*time.Second)) // inside 30s window
	var saw string
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, oldAccess, "refresh-1"))
	rec := doAuth(h, r, echoNext(t, &saw))
	if rec.Code != http.StatusOK || saw != newAccess {
		t.Fatalf("code=%d sawNew=%v", rec.Code, saw == newAccess)
	}
	var got *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.CookieName {
			got = c
		}
	}
	if got == nil || got.MaxAge != 1000 {
		t.Fatalf("re-sealed cookie = %+v", got)
	}
	opened, err := h.codec.Open(got.Value)
	if err != nil || opened.RefreshToken != "refresh-2" {
		t.Fatalf("opened = %+v err=%v", opened, err)
	}
	if fc.results[session.RefreshKey("refresh-1")] == "" {
		t.Fatal("rotation result should be published for concurrent requests")
	}
}

func TestReuseRevocationDenylistsAndClears(t *testing.T) {
	fc := newStubCache()
	h := newTestHandlers(t, fc, &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
		return authclient.TokenPair{}, &authclient.ReusedError{RevokeJTIs: []string{"jA", "jB"}}
	}})
	access := mintAccess(t, "u1", "j1", time.Now().Add(10*time.Second))
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, access, "refresh-1"))
	rec := doAuth(h, r, echoNext(t, new(string)))
	if rec.Code != http.StatusUnauthorized || !clearedCookie(rec) {
		t.Fatalf("code=%d cleared=%v", rec.Code, clearedCookie(rec))
	}
	if len(fc.denyAdds) != 1 || len(fc.denyAdds[0]) != 2 {
		t.Fatalf("denylist adds = %v", fc.denyAdds)
	}
}

func TestUserUnavailableDegrades(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
		return authclient.TokenPair{}, authclient.ErrUserUnavailable
	}})

	// Still-valid token: serve the request on it; retry refresh later.
	valid := mintAccess(t, "u1", "j1", time.Now().Add(10*time.Second))
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, valid, "refresh-1"))
	rec := doAuth(h, r, echoNext(t, new(string)))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token should degrade gracefully, code = %d", rec.Code)
	}

	// Expired token: 503, cookie KEPT (the refresh token was not consumed).
	expired := mintAccess(t, "u1", "j1", time.Now().Add(-time.Second))
	r = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, expired, "refresh-1"))
	rec = doAuth(h, r, echoNext(t, new(string)))
	if rec.Code != http.StatusServiceUnavailable || clearedCookie(rec) {
		t.Fatalf("code=%d cleared=%v (want 503, cookie kept)", rec.Code, clearedCookie(rec))
	}
}

func TestAuthOutageDegrades(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
		return authclient.TokenPair{}, errors.New("connection refused")
	}})
	valid := mintAccess(t, "u1", "j1", time.Now().Add(10*time.Second))
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, valid, "refresh-1"))
	if rec := doAuth(h, r, echoNext(t, new(string))); rec.Code != http.StatusOK {
		t.Fatalf("valid token + auth outage should degrade, code = %d", rec.Code)
	}
	expired := mintAccess(t, "u1", "j1", time.Now().Add(-time.Second))
	r = httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, expired, "refresh-1"))
	if rec := doAuth(h, r, echoNext(t, new(string))); rec.Code != http.StatusBadGateway {
		t.Fatalf("expired token + auth outage: code = %d, want 502", rec.Code)
	}
}

func TestConcurrentRefreshIsSingleflight(t *testing.T) {
	fc := newStubCache()
	var calls atomic.Int32
	newAccess := mintAccess(t, "u1", "j2", time.Now().Add(5*time.Minute))
	h := newTestHandlers(t, fc, &stubAuth{refresh: func(context.Context, string) (authclient.TokenPair, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond) // hold the lock while others arrive
		return authclient.TokenPair{AccessToken: newAccess, RefreshToken: "refresh-2",
			ExpiresIn: 300, RefreshExpiresIn: 1000}, nil
	}})
	oldAccess := mintAccess(t, "u1", "j1", time.Now().Add(10*time.Second)) // expiring but VALID
	cookie := sealedCookie(t, h, oldAccess, "refresh-1")

	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
			r.AddCookie(cookie)
			codes[i] = doAuth(h, r, echoNext(t, new(string))).Code
		}(i)
	}
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("request %d: code = %d", i, code)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("refresh ran %d times; the lock must make it exactly 1", n)
	}
}

func TestExpiredWaiterAdoptsPublishedResult(t *testing.T) {
	fc := newStubCache()
	h := newTestHandlers(t, fc, &stubAuth{}) // Refresh would panic: adopter must not rotate
	expired := mintAccess(t, "u1", "j1", time.Now().Add(-time.Second))
	cookie := sealedCookie(t, h, expired, "refresh-1")
	key := session.RefreshKey("refresh-1")

	// Simulate a concurrent rotation: lock held, result published.
	fc.locks[key] = "someone-else"
	newAccess := mintAccess(t, "u1", "j2", time.Now().Add(5*time.Minute))
	sealed, err := h.codec.Seal(session.Session{AccessToken: newAccess, RefreshToken: "refresh-2"})
	if err != nil {
		t.Fatal(err)
	}
	fc.results[key] = mustResultJSON(t, sealed, 1000)

	var saw string
	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(cookie)
	rec := doAuth(h, r, echoNext(t, &saw))
	if rec.Code != http.StatusOK || saw != newAccess {
		t.Fatalf("code=%d adopted=%v", rec.Code, saw == newAccess)
	}
	if !strings.Contains(rec.Header().Get("Set-Cookie"), session.CookieName+"=") {
		t.Fatal("adopter should set the published cookie")
	}
}

func TestExpiredWaiterTimesOut(t *testing.T) {
	fc := newStubCache()
	h := newTestHandlers(t, fc, &stubAuth{})
	expired := mintAccess(t, "u1", "j1", time.Now().Add(-time.Second))
	key := session.RefreshKey("refresh-1")
	fc.locks[key] = "someone-else" // holder never publishes

	r := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r.AddCookie(sealedCookie(t, h, expired, "refresh-1"))
	rec := doAuth(h, r, echoNext(t, new(string)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d", rec.Code)
	}
	if clearedCookie(rec) {
		t.Fatal("timeout must NOT clear the cookie: the rotation may still land for the next request")
	}
}

func TestCheckOrigin(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	do := func(method, origin, sfs string) int {
		r := httptest.NewRequest(method, "/api/auth/logout", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if sfs != "" {
			r.Header.Set("Sec-Fetch-Site", sfs)
		}
		rec := httptest.NewRecorder()
		h.CheckOrigin(next).ServeHTTP(rec, r)
		return rec.Code
	}
	if c := do(http.MethodPost, "http://localhost:5173", ""); c != http.StatusOK {
		t.Errorf("allowed origin: %d", c)
	}
	if c := do(http.MethodPost, "https://evil.example", ""); c != http.StatusForbidden {
		t.Errorf("foreign origin: %d", c)
	}
	if c := do(http.MethodPost, "", "cross-site"); c != http.StatusForbidden {
		t.Errorf("cross-site fetch metadata: %d", c)
	}
	if c := do(http.MethodPost, "", "same-origin"); c != http.StatusOK {
		t.Errorf("same-origin fetch metadata: %d", c)
	}
	if c := do(http.MethodPost, "", ""); c != http.StatusOK {
		t.Errorf("non-browser client (no headers): %d", c)
	}
	if c := do(http.MethodGet, "https://evil.example", ""); c != http.StatusOK {
		t.Errorf("GET is never origin-checked: %d", c)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q", header, got)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("csp = %q", csp)
	}
}

func TestAuthenticateGuardsNonNormalizedPaths(t *testing.T) {
	h := newTestHandlers(t, newStubCache(), &stubAuth{})
	// A protected path in non-normalized form must still require a
	// session at THIS guard (no reliance on the router to normalize).
	for _, raw := range []string{"/./api/me", "//api/me", "/api/../api/me"} {
		ran := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true })
		rec := httptest.NewRecorder()
		// Build the request with the raw path preserved.
		req := httptest.NewRequest(http.MethodGet, "http://x"+raw, nil)
		req.URL.Path = raw // url.Parse may normalize; force the raw value
		h.Authenticate(next).ServeHTTP(rec, req)
		if ran || rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: ran=%v code=%d (must be guarded, want 401)", raw, ran, rec.Code)
		}
	}
	// An allowlisted path in non-normalized form still bypasses.
	ran := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ran = true })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://x/api/auth/../auth/login", nil)
	req.URL.Path = "/api/auth/../auth/login" // force the raw value
	h.Authenticate(next).ServeHTTP(rec, req)
	if !ran {
		t.Errorf("cleaned allowlisted path should bypass auth")
	}
}

func TestStaleRequestAfterRotationAdoptsNotReRefresh(t *testing.T) {
	fc := newStubCache()
	var calls atomic.Int32
	var mu sync.Mutex
	rotated := map[string]bool{}
	newAccess := mintAccess(t, "u1", "j2", time.Now().Add(5*time.Minute))
	h := newTestHandlers(t, fc, &stubAuth{refresh: func(_ context.Context, rt string) (authclient.TokenPair, error) {
		calls.Add(1)
		mu.Lock()
		defer mu.Unlock()
		if rotated[rt] {
			// The real auth service revokes the family when a consumed
			// refresh token is presented again.
			return authclient.TokenPair{}, &authclient.ReusedError{RevokeJTIs: []string{"j1"}}
		}
		rotated[rt] = true
		return authclient.TokenPair{AccessToken: newAccess, RefreshToken: "refresh-2",
			ExpiresIn: 300, RefreshExpiresIn: 1000}, nil
	}})
	expiring := mintAccess(t, "u1", "j1", time.Now().Add(10*time.Second)) // within window, still valid
	cookie := sealedCookie(t, h, expiring, "refresh-1")

	// Request 1 rotates refresh-1 -> refresh-2, publishes, releases the lock.
	r1 := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r1.AddCookie(cookie)
	if rec := doAuth(h, r1, echoNext(t, new(string))); rec.Code != http.StatusOK {
		t.Fatalf("first request code = %d", rec.Code)
	}

	// Request 2 still bears the consumed refresh-1 (its cookie had not
	// updated). It must adopt the published result, never re-refresh.
	var saw string
	r2 := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r2.AddCookie(cookie)
	rec2 := doAuth(h, r2, echoNext(t, &saw))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second request code = %d, want 200 (must adopt, not re-refresh)", rec2.Code)
	}
	if saw != newAccess {
		t.Fatal("second request should serve the rotated access token")
	}
	if clearedCookie(rec2) {
		t.Fatal("second request must not clear the session")
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("auth.Refresh ran %d times; the consumed token must not be re-refreshed", n)
	}
}

// TestConcurrentRefreshHitsAuthOnce: N concurrent requests
// bearing the same expiring-but-valid token must trigger exactly one
// rotation, because real Valkey SETNX makes the refresh singleflight.
func TestConcurrentRefreshHitsAuthOnce(t *testing.T) {
	s := newStack(t)
	s.auth.refreshDelay = 20 * time.Millisecond // hold the lock while others arrive
	// sub must be a UUID: /api/me's userclient parses it as one when
	// composing the profile after the (winner's) rotation.
	const sub = "22222222-2222-2222-2222-222222222222"
	access := mintAccess(t, sub, "jA", time.Now().Add(10*time.Second))
	s.auth.seedChain("refresh-1", sub, "jA")
	s.users.id, s.users.email, s.users.handle = sub, "u1@example.test", "u-one"
	cookie := s.cookieFor(t, access, "refresh-1")

	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = s.getMe(t, cookie).status
		}(i)
	}
	wg.Wait()

	for i, code := range codes {
		if code != http.StatusOK {
			t.Errorf("request %d: status = %d, want 200", i, code)
		}
	}
	if got := s.auth.refreshCalls.Load(); got != 1 {
		t.Fatalf("auth /token/refresh served %d times; real-Valkey SETNX must make it exactly 1", got)
	}
}

// TestStaleTokenAdoptsAfterRotation is the
// headline: the real-Valkey version of the reuse-window regression.
// Request 1 rotates refresh-1 -> refresh-2 and publishes to Valkey.
// Request 2, still bearing the consumed refresh-1, must ADOPT the
// published successor from real Valkey (keyed by sha256(refresh-1))
// instead of presenting the consumed token to auth a second time. If it
// re-refreshed, the stub auth would answer refresh_reused and the bff
// would 401 + clear; this asserts that does NOT happen.
func TestStaleTokenAdoptsAfterRotation(t *testing.T) {
	s := newStack(t)
	// sub must be a UUID: /api/me composes the profile via the userclient
	// (which parses sub as a UUID) on both requests.
	const sub = "33333333-3333-3333-3333-333333333333"
	access := mintAccess(t, sub, "jA", time.Now().Add(10*time.Second)) // within the 30s window, still valid
	s.auth.seedChain("refresh-1", sub, "jA")
	s.users.id, s.users.email, s.users.handle = sub, "u1@example.test", "u-one"
	cookie := s.cookieFor(t, access, "refresh-1")

	// Request 1 rotates and publishes the successor to real Valkey.
	if r1 := s.getMe(t, cookie); r1.status != http.StatusOK {
		t.Fatalf("request 1 status = %d, want 200", r1.status)
	}
	if got := s.auth.refreshCalls.Load(); got != 1 {
		t.Fatalf("after request 1, refresh calls = %d, want 1", got)
	}

	// Request 2 still bears the consumed refresh-1. It must adopt the
	// published result, never re-present the consumed token.
	r2 := s.getMe(t, cookie)
	if r2.status != http.StatusOK {
		t.Fatalf("request 2 status = %d, want 200 (must adopt the rotated session, not re-refresh)", r2.status)
	}
	if r2.cleared {
		t.Fatal("request 2 must not clear the session (adoption succeeded)")
	}
	if got := s.auth.refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh served %d times; the consumed refresh-1 must never be presented to auth twice", got)
	}
}

// TestReuseDenylistsAndRejects drives the reuse
// path's real-Valkey denylist write and the denylist read that rejects
// a stolen access token. Step A: a refresh of an already-consumed token
// returns refresh_reused; the bff writes the reported jti to the real
// Valkey denylist and clears+401. Step B: a fresh, otherwise-valid
// cookie whose access jti equals that denylisted jti is rejected by the
// real-Valkey EXISTS check before any refresh.
func TestReuseDenylistsAndRejects(t *testing.T) {
	s := newStack(t)
	const sub = "44444444-4444-4444-4444-444444444444"
	s.users.id, s.users.email, s.users.handle = sub, "u1@example.test", "u-one"

	// Step A: expiring-valid access (jti jA) on an already-consumed
	// refresh token whose live jti is jLive.
	accessA := mintAccess(t, sub, "jA", time.Now().Add(10*time.Second))
	s.auth.markConsumed("already-consumed", sub, "jLive")
	rA := s.getMe(t, s.cookieFor(t, accessA, "already-consumed"))
	if rA.status != http.StatusUnauthorized || !rA.cleared {
		t.Fatalf("step A: status=%d cleared=%v, want 401 + cleared", rA.status, rA.cleared)
	}

	// Step B: a fresh, valid cookie whose access jti is the now-denylisted
	// jLive (exp far out, so no refresh is attempted) must be rejected by
	// the real-Valkey denylist read. Snapshot the cumulative refresh count
	// first: step A spent exactly one refresh; step B must add none.
	before := s.auth.refreshCalls.Load()
	accessLive := mintAccess(t, sub, "jLive", time.Now().Add(5*time.Minute))
	rB := s.getMe(t, s.cookieFor(t, accessLive, "refresh-unused"))
	if rB.status != http.StatusUnauthorized || !rB.cleared {
		t.Fatalf("step B: status=%d cleared=%v, want 401 + cleared (denylist EXISTS hit)", rB.status, rB.cleared)
	}
	if got := s.auth.refreshCalls.Load() - before; got != 0 {
		t.Fatalf("step B refreshed %d times; the real-Valkey denylist must reject before any refresh", got)
	}
}

// TestSearchPassThroughReachesEnrichmentWithBearerNeverCached drives the
// cookie-authenticated search route through the real server and the
// real enrichmentclient (over real HTTP) against a stub enrichment
// service, proving the session's own bearer rides the proxied call and
// that the route is never cached at the bff: two identical calls must
// reach the upstream twice, not once.
func TestSearchPassThroughReachesEnrichmentWithBearerNeverCached(t *testing.T) {
	s := newStack(t)
	const sub = "55555555-5555-5555-5555-555555555555"
	access := mintAccess(t, sub, "jA", time.Now().Add(5*time.Minute)) // fresh: no refresh
	cookie := s.cookieFor(t, access, "refresh-1")

	r1 := s.search(t, cookie, "game", "chrono")
	if r1.status != http.StatusOK || !strings.Contains(string(r1.body), "Chrono Trigger") {
		t.Fatalf("first call: status=%d body=%s", r1.status, r1.body)
	}

	r2 := s.search(t, cookie, "game", "chrono")
	if r2.status != http.StatusOK {
		t.Fatalf("second call: status = %d body=%s", r2.status, r2.body)
	}

	if got := s.enrich.callCount(); got != 2 {
		t.Fatalf("enrichment was hit %d times; two identical calls must never be served from a bff cache (single-source pass-through)", got)
	}
	if len(s.enrich.gotAuth) != 2 || s.enrich.gotAuth[0] != "Bearer "+access || s.enrich.gotAuth[1] != "Bearer "+access {
		t.Fatalf("bearer forwarded to enrichment = %v, want the session's own token on both calls", s.enrich.gotAuth)
	}
}

// TestEntriesPassThroughHitsCollectionTwiceNeverCached drives the
// cookie-authenticated entries route through the real server and the
// real collectionclient (over real HTTP) against a stub collection
// service, proving the session's own bearer rides the proxied call and
// that the route is never cached at the bff: two identical calls must
// reach the upstream twice, not once.
func TestEntriesPassThroughHitsCollectionTwiceNeverCached(t *testing.T) {
	s := newStack(t)
	const sub = "66666666-6666-6666-6666-666666666666"
	access := mintAccess(t, sub, "jA", time.Now().Add(5*time.Minute)) // fresh: no refresh
	cookie := s.cookieFor(t, access, "refresh-1")

	r1 := s.entries(t, cookie)
	if r1.status != http.StatusOK {
		t.Fatalf("first call: status=%d body=%s", r1.status, r1.body)
	}

	r2 := s.entries(t, cookie)
	if r2.status != http.StatusOK {
		t.Fatalf("second call: status = %d body=%s", r2.status, r2.body)
	}

	if got := s.collection.callCount(); got != 2 {
		t.Fatalf("collection was hit %d times; two identical calls must never be served from a bff cache (single-source pass-through)", got)
	}
	if len(s.collection.gotAuth) != 2 || s.collection.gotAuth[0] != "Bearer "+access || s.collection.gotAuth[1] != "Bearer "+access {
		t.Fatalf("bearer forwarded to collection = %v, want the session's own token on both calls", s.collection.gotAuth)
	}
}

// TestValueHistoryPassThroughHitsCollectionTwiceNeverCached drives the
// cookie-authenticated value-history route through the real server and
// the real collectionclient (over real HTTP) against a stub collection
// service, proving that the route is never cached at the bff: two
// identical calls must reach the upstream twice, not once. This pins
// the single-source rule for the new route (the collection service
// owns the cache; the bff must never shadow it).
func TestValueHistoryPassThroughHitsCollectionTwiceNeverCached(t *testing.T) {
	s := newStack(t)
	const sub = "88888888-8888-8888-8888-888888888888"
	access := mintAccess(t, sub, "jA", time.Now().Add(5*time.Minute)) // fresh: no refresh
	cookie := s.cookieFor(t, access, "refresh-1")
	const wantBody = `{"available":true,"points":[]}`

	r1 := s.valueHistory(t, cookie)
	if r1.status != http.StatusOK || string(r1.body) != wantBody {
		t.Fatalf("first call: status=%d body=%s", r1.status, r1.body)
	}

	r2 := s.valueHistory(t, cookie)
	if r2.status != http.StatusOK || string(r2.body) != wantBody {
		t.Fatalf("second call: status = %d body=%s", r2.status, r2.body)
	}

	if got := s.collection.valueHistoryHitCount(); got != 2 {
		t.Fatalf("collection was hit %d times; two identical calls must never be served from a bff cache (single-source pass-through)", got)
	}
}

// TestRecommendationsCacheLifecycle drives the composed
// /api/recommendations route against real Valkey: two reads must cost
// exactly one upstream score call (the second is a real-Valkey cache
// hit), and a subsequent entry mutation must invalidate that cache so
// the next read recomposes - the genuine guard against real SETNX/DEL
// semantics that a stub cache could fake.
func TestRecommendationsCacheLifecycle(t *testing.T) {
	s := newStack(t)
	const sub = "77777777-7777-7777-7777-777777777777"
	access := mintAccess(t, sub, "jA", time.Now().Add(5*time.Minute)) // fresh: no refresh
	cookie := s.cookieFor(t, access, "refresh-1")

	r1 := s.recommendations(t, cookie)
	if r1.status != http.StatusOK {
		t.Fatalf("first call: status=%d body=%s", r1.status, r1.body)
	}
	r2 := s.recommendations(t, cookie)
	if r2.status != http.StatusOK {
		t.Fatalf("second call: status=%d body=%s", r2.status, r2.body)
	}
	if got := s.enrich.scoreCallCount(); got != 1 {
		t.Fatalf("score was hit %d times over two reads; want 1 (real-Valkey cache hit on the second)", got)
	}

	if code := s.createEntry(t, cookie); code != http.StatusCreated {
		t.Fatalf("entry creation: status = %d", code)
	}

	r3 := s.recommendations(t, cookie)
	if r3.status != http.StatusOK {
		t.Fatalf("third call: status=%d body=%s", r3.status, r3.body)
	}
	if got := s.enrich.scoreCallCount(); got != 2 {
		t.Fatalf("score was hit %d times after the mutation; want 2 (real-Valkey DEL must invalidate the cache)", got)
	}
	if got := s.collection.createCallCount(); got != 1 {
		t.Fatalf("collection create-entry was hit %d times; want 1", got)
	}
}

// TestOtlpProxyRelaysWithSessionAndNeverCaches drives the cookie-
// authenticated otlp relay route through the real server against a stub
// collector, proving two identical calls both reach the collector (a
// pass-through is never cached at the bff, exactly like every other
// relay route) and that a cookieless POST is rejected before the
// collector is ever dialed.
func TestOtlpProxyRelaysWithSessionAndNeverCaches(t *testing.T) {
	s := newStack(t)
	const sub = "99999999-9999-9999-9999-999999999999"
	access := mintAccess(t, sub, "jA", time.Now().Add(5*time.Minute)) // fresh: no refresh
	cookie := s.cookieFor(t, access, "refresh-1")

	if code := s.otlpTraces(t, cookie); code != http.StatusOK {
		t.Fatalf("first call: status = %d", code)
	}
	if code := s.otlpTraces(t, cookie); code != http.StatusOK {
		t.Fatalf("second call: status = %d", code)
	}
	if got := s.otlp.hitCount(); got != 2 {
		t.Fatalf("collector was hit %d times; two identical calls must never be served from a bff cache (single-source pass-through)", got)
	}

	if code := s.otlpTraces(t, nil); code != http.StatusUnauthorized {
		t.Fatalf("cookieless call: status = %d, want 401", code)
	}
	if got := s.otlp.hitCount(); got != 2 {
		t.Fatalf("cookieless call must not reach the collector; hits = %d, want 2", got)
	}
}
