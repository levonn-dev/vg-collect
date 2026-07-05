package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/levonn-dev/vg-collect/libs/go/valkeykit"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/cache"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/db"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/store"
	"github.com/levonn-dev/vg-collect/services/enrichment/migrations"
)

// ---------------------------------------------------------------
// Stub doubles (function fields; a nil field panics loudly).
// ---------------------------------------------------------------

// stubStore implements Store via function fields.
type stubStore struct {
	findProduct        func(ctx context.Context, key store.ProductKey) (store.Product, error)
	createProduct      func(ctx context.Context, p store.Product) (store.Product, error)
	getProduct         func(ctx context.Context, id string) (store.Product, error)
	setIGDB            func(ctx context.Context, id string, m store.IGDBMeta) error
	setPriceCharting   func(ctx context.Context, id string, m *store.PCMeta) error
	setCurrentPrices   func(ctx context.Context, id string, q store.PriceQuote, asOf time.Time) error
	listPriced         func(ctx context.Context) ([]store.Product, error)
	productsByIDs      func(ctx context.Context, ids []string) ([]store.Product, error)
	searchByName       func(ctx context.Context, q string, limit int) ([]store.Product, error)
	upsertRaw          func(ctx context.Context, games []igdb.Game, fetchedAt time.Time) error
	rawByIDs           func(ctx context.Context, ids []int64) ([]store.RawGame, error)
	upsertPlatforms    func(ctx context.Context, ps []igdb.Platform, fetchedAt time.Time) error
	listPlatforms      func(ctx context.Context) ([]igdb.Platform, error)
	platformsFetchedAt func(ctx context.Context) (time.Time, error)
	appendSnapshot     func(ctx context.Context, s store.Snapshot) error
}

var _ Store = (*stubStore)(nil)

func (s *stubStore) FindProduct(ctx context.Context, key store.ProductKey) (store.Product, error) {
	if s.findProduct == nil {
		panic("unexpected FindProduct")
	}
	return s.findProduct(ctx, key)
}

func (s *stubStore) CreateProduct(ctx context.Context, p store.Product) (store.Product, error) {
	if s.createProduct == nil {
		panic("unexpected CreateProduct")
	}
	return s.createProduct(ctx, p)
}

func (s *stubStore) GetProduct(ctx context.Context, id string) (store.Product, error) {
	if s.getProduct == nil {
		panic("unexpected GetProduct")
	}
	return s.getProduct(ctx, id)
}

func (s *stubStore) SetIGDB(ctx context.Context, id string, m store.IGDBMeta) error {
	if s.setIGDB == nil {
		panic("unexpected SetIGDB")
	}
	return s.setIGDB(ctx, id, m)
}

func (s *stubStore) SetPriceCharting(ctx context.Context, id string, m *store.PCMeta) error {
	if s.setPriceCharting == nil {
		panic("unexpected SetPriceCharting")
	}
	return s.setPriceCharting(ctx, id, m)
}

func (s *stubStore) SetCurrentPrices(ctx context.Context, id string, q store.PriceQuote, asOf time.Time) error {
	if s.setCurrentPrices == nil {
		panic("unexpected SetCurrentPrices")
	}
	return s.setCurrentPrices(ctx, id, q, asOf)
}

func (s *stubStore) ListPriced(ctx context.Context) ([]store.Product, error) {
	if s.listPriced == nil {
		panic("unexpected ListPriced")
	}
	return s.listPriced(ctx)
}

func (s *stubStore) ProductsByIDs(ctx context.Context, ids []string) ([]store.Product, error) {
	if s.productsByIDs == nil {
		panic("unexpected ProductsByIDs")
	}
	return s.productsByIDs(ctx, ids)
}

func (s *stubStore) SearchByName(ctx context.Context, q string, limit int) ([]store.Product, error) {
	if s.searchByName == nil {
		panic("unexpected SearchByName")
	}
	return s.searchByName(ctx, q, limit)
}

func (s *stubStore) UpsertRaw(ctx context.Context, games []igdb.Game, fetchedAt time.Time) error {
	if s.upsertRaw == nil {
		panic("unexpected UpsertRaw")
	}
	return s.upsertRaw(ctx, games, fetchedAt)
}

func (s *stubStore) RawByIDs(ctx context.Context, ids []int64) ([]store.RawGame, error) {
	if s.rawByIDs == nil {
		panic("unexpected RawByIDs")
	}
	return s.rawByIDs(ctx, ids)
}

func (s *stubStore) UpsertPlatforms(ctx context.Context, ps []igdb.Platform, fetchedAt time.Time) error {
	if s.upsertPlatforms == nil {
		panic("unexpected UpsertPlatforms")
	}
	return s.upsertPlatforms(ctx, ps, fetchedAt)
}

func (s *stubStore) ListPlatforms(ctx context.Context) ([]igdb.Platform, error) {
	if s.listPlatforms == nil {
		panic("unexpected ListPlatforms")
	}
	return s.listPlatforms(ctx)
}

func (s *stubStore) PlatformsFetchedAt(ctx context.Context) (time.Time, error) {
	if s.platformsFetchedAt == nil {
		panic("unexpected PlatformsFetchedAt")
	}
	return s.platformsFetchedAt(ctx)
}

func (s *stubStore) AppendSnapshot(ctx context.Context, snap store.Snapshot) error {
	if s.appendSnapshot == nil {
		panic("unexpected AppendSnapshot")
	}
	return s.appendSnapshot(ctx, snap)
}

// stubGames implements GameProvider via function fields.
type stubGames struct {
	searchGames  func(ctx context.Context, q string, limit int) ([]igdb.Game, error)
	gamesByIDs   func(ctx context.Context, ids []int64) ([]igdb.Game, error)
	popularGames func(ctx context.Context, genreIDs []int64, excludeIDs []int64, limit int) ([]igdb.Game, error)
	platforms    func(ctx context.Context) ([]igdb.Platform, error)
}

var _ GameProvider = (*stubGames)(nil)

func (s *stubGames) SearchGames(ctx context.Context, q string, limit int) ([]igdb.Game, error) {
	if s.searchGames == nil {
		panic("unexpected SearchGames")
	}
	return s.searchGames(ctx, q, limit)
}

func (s *stubGames) GamesByIDs(ctx context.Context, ids []int64) ([]igdb.Game, error) {
	if s.gamesByIDs == nil {
		panic("unexpected GamesByIDs")
	}
	return s.gamesByIDs(ctx, ids)
}

func (s *stubGames) PopularGames(ctx context.Context, genreIDs []int64, excludeIDs []int64, limit int) ([]igdb.Game, error) {
	if s.popularGames == nil {
		panic("unexpected PopularGames")
	}
	return s.popularGames(ctx, genreIDs, excludeIDs, limit)
}

func (s *stubGames) Platforms(ctx context.Context) ([]igdb.Platform, error) {
	if s.platforms == nil {
		panic("unexpected Platforms")
	}
	return s.platforms(ctx)
}

// stubPrices implements PriceProvider via function fields.
type stubPrices struct {
	search  func(ctx context.Context, q string) ([]pricecharting.Product, error)
	product func(ctx context.Context, id int64) (pricecharting.Product, error)
}

var _ PriceProvider = (*stubPrices)(nil)

func (s *stubPrices) Search(ctx context.Context, q string) ([]pricecharting.Product, error) {
	if s.search == nil {
		panic("unexpected Search")
	}
	return s.search(ctx, q)
}

func (s *stubPrices) Product(ctx context.Context, id int64) (pricecharting.Product, error) {
	if s.product == nil {
		panic("unexpected Product")
	}
	return s.product(ctx, id)
}

// stubCache is an in-memory Cache; err poisons every method to drive
// the fail-open branches.
type stubCache struct {
	err    error
	search map[string][]byte
	prods  map[string][]byte
	puts   int
}

var _ Cache = (*stubCache)(nil)

func newStubCache() *stubCache {
	return &stubCache{search: map[string][]byte{}, prods: map[string][]byte{}}
}

func (c *stubCache) GetSearch(_ context.Context, kind, q string) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.search[kind+":"+q], nil
}

func (c *stubCache) PutSearch(_ context.Context, kind, q string, body []byte, _ time.Duration) error {
	if c.err != nil {
		return c.err
	}
	c.puts++
	c.search[kind+":"+q] = body
	return nil
}

func (c *stubCache) GetProduct(_ context.Context, id string) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.prods[id], nil
}

func (c *stubCache) PutProduct(_ context.Context, id string, body []byte, _ time.Duration) error {
	if c.err != nil {
		return c.err
	}
	c.puts++
	c.prods[id] = body
	return nil
}

func (c *stubCache) InvalidateProduct(_ context.Context, id string) error {
	if c.err != nil {
		return c.err
	}
	delete(c.prods, id)
	return nil
}

// newUnitHandlers builds Handlers over stubs with fast defaults.
func newUnitHandlers(st Store, games GameProvider, prices PriceProvider, c Cache) *Handlers {
	return New(st, games, prices, c, Options{
		SearchCacheTTL:         time.Hour,
		ProductCacheTTL:        time.Minute,
		IGDBRefreshAfter:       720 * time.Hour,
		InternalRefreshSecrets: []string{testInternalToken},
		Logger:                 slog.New(slog.DiscardHandler),
	})
}

// serveUnit routes one request through the real router + jwtauth.
func serveUnit(t *testing.T, h *Handlers, env *authEnv, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router := NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil })
	router.ServeHTTP(rec, req)
	return rec
}

// ---------------------------------------------------------------
// Integration stack: real Mongo + Valkey + fixture providers behind
// the real router. Skipped under -short.
// ---------------------------------------------------------------

type stack struct {
	t      *testing.T
	srv    *httptest.Server
	env    *authEnv
	h      *Handlers
	store  *store.Store
	mdb    *mongo.Database
	client *http.Client
}

func newStack(t *testing.T) *stack {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()

	mc, err := tcmongo.Run(ctx, "mongo:8")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Terminate(ctx) })
	murl, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, murl, "enrichment", migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mclient, err := db.Connect(ctx, murl)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mclient.Disconnect(context.Background()) })
	mdb := mclient.Database("enrichment")
	st := store.New(mdb)

	vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vk.Terminate(ctx) })
	vurl, err := vk.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rdb, err := valkeykit.Connect(ctx, vurl)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })

	games, err := igdb.NewStub()
	if err != nil {
		t.Fatal(err)
	}
	prices, err := pricecharting.NewStub()
	if err != nil {
		t.Fatal(err)
	}
	env := newAuthEnv(t)
	h := New(st, games, prices, cache.New(rdb), Options{
		SearchCacheTTL:         time.Hour,
		ProductCacheTTL:        time.Minute,
		IGDBRefreshAfter:       720 * time.Hour,
		InternalRefreshSecrets: []string{testInternalToken},
		Logger:                 slog.New(slog.DiscardHandler),
	})
	router := NewRouter(h, env.validator(), slog.New(slog.DiscardHandler),
		func(c context.Context) error { return db.Health(c, mclient) })
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &stack{t: t, srv: srv, env: env, h: h, store: st, mdb: mdb, client: srv.Client()}
}

func (s *stack) userToken() string {
	return s.env.token(s.t, "11111111-1111-1111-1111-111111111111", []string{"user"})
}

func (s *stack) adminToken() string {
	return s.env.token(s.t, "22222222-2222-2222-2222-222222222222", []string{"user", "admin"})
}

// do sends a request with an optional Bearer token and JSON body.
func (s *stack) do(method, path, token string, body any) *http.Response {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, s.srv.URL+path, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp
}

func decodeBody[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var v T
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// resolveGame is the shared shortcut used across the handler tests
// (it exercises the resolve handler once that handler exists; it
// provides a unified product interface for resolve-dependent tests).
func (s *stack) resolveGame(igdbGameID, platformID int64) api.Product {
	s.t.Helper()
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": igdbGameID, "platform_igdb_id": platformID,
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("resolve: %d %s", resp.StatusCode, b)
	}
	return decodeBody[api.Product](s.t, resp)
}

// ---------------------------------------------------------------
// Task-scope tests: search + product read-through
// ---------------------------------------------------------------

func TestSearch_GameThroughStubAndCache(t *testing.T) {
	s := newStack(t)

	resp := s.do(http.MethodGet, "/search?type=game&q=zelda", s.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: %d", resp.StatusCode)
	}
	res := decodeBody[api.SearchResults](t, resp)
	if res.Degraded || len(res.Results) != 4 {
		t.Fatalf("want 4 non-degraded zelda hits, got %d (degraded=%v)", len(res.Results), res.Degraded)
	}
	first := res.Results[0]
	if string(first.Type) != "game" || first.IgdbGameId == nil || first.Platforms == nil || first.CoverUrl == nil {
		t.Fatalf("game result shape: %+v", first)
	}
	wantOoTDate := time.Date(1998, time.November, 21, 0, 0, 0, 0, time.UTC)
	if first.FirstReleaseDate == nil || !first.FirstReleaseDate.Equal(wantOoTDate) {
		t.Fatalf("game result first_release_date: %+v", first.FirstReleaseDate)
	}

	// Second identical query must come from Valkey: poison the
	// provider and expect the same non-degraded answer.
	s.h.games = &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return nil, errors.New("provider must not be called on a cache hit")
	}}
	resp = s.do(http.MethodGet, "/search?type=game&q=ZELDA", s.userToken(), nil)
	res = decodeBody[api.SearchResults](t, resp)
	if res.Degraded || len(res.Results) != 4 {
		t.Fatalf("cache hit broken: %d (degraded=%v)", len(res.Results), res.Degraded)
	}
}

func TestSearch_HardwareFiltersToHardware(t *testing.T) {
	s := newStack(t)
	resp := s.do(http.MethodGet, "/search?type=hardware&q=nintendo+64", s.userToken(), nil)
	res := decodeBody[api.SearchResults](t, resp)
	if len(res.Results) != 2 {
		t.Fatalf("want the N64 system + controller, got %d: %+v", len(res.Results), res.Results)
	}
	for _, r := range res.Results {
		if string(r.Type) != "hardware" || r.PcProductId == nil || r.ConsoleName == nil || r.Category == nil {
			t.Fatalf("hardware result shape: %+v", r)
		}
		// Game products on that console must not leak into hardware
		// search (Mario Kart 64 etc. carry no hardware category).
		if *r.Category != "Systems" && *r.Category != "Controllers" {
			t.Fatalf("unexpected category: %+v", r)
		}
	}
}

func TestUnitSearch_DegradedFallsBackToCatalog(t *testing.T) {
	env := newAuthEnv(t)
	releaseDate := time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)
	st := &stubStore{searchByName: func(_ context.Context, q string, _ int) ([]store.Product, error) {
		return []store.Product{{
			ID: "11111111-1111-1111-1111-111111111111", Type: "game", Name: "Chrono Trigger",
			Platform: &store.Platform{IGDBID: 19, Name: "Super Nintendo Entertainment System"},
			IGDB:     &store.IGDBMeta{GameID: 1011, FetchedAt: time.Now(), FirstReleaseDate: releaseDate},
		}}, nil
	}}
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return nil, errors.New("igdb down")
	}}
	h := newUnitHandlers(st, games, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("degraded search: %d %s", rec.Code, rec.Body.String())
	}
	var res api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Degraded || len(res.Results) != 1 || res.Results[0].Name != "Chrono Trigger" {
		t.Fatalf("degraded result: %+v", res)
	}
	if got := res.Results[0].FirstReleaseDate; got == nil || !got.Equal(releaseDate) {
		t.Fatalf("degraded result first_release_date: %+v", got)
	}
}

func TestUnitSearch_DegradedIsNotCached(t *testing.T) {
	env := newAuthEnv(t)
	c := newStubCache()
	st := &stubStore{searchByName: func(context.Context, string, int) ([]store.Product, error) { return nil, nil }}
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return nil, errors.New("igdb down")
	}}
	h := newUnitHandlers(st, games, nil, c)
	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=zzz", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK || c.puts != 0 {
		t.Fatalf("degraded answers must not be cached: %d, puts=%d", rec.Code, c.puts)
	}
}

func TestUnitSearch_CacheFailureFailsOpen(t *testing.T) {
	env := newAuthEnv(t)
	c := newStubCache()
	c.err = errors.New("valkey down")
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1011, Name: "Chrono Trigger"}}, nil
	}}
	h := newUnitHandlers(nil, games, nil, c)
	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cache outage must not fail search: %d", rec.Code)
	}
}

func TestUnitSearch_BadParams(t *testing.T) {
	env := newAuthEnv(t)
	h := newUnitHandlers(nil, nil, nil, newStubCache())
	tok := env.token(t, "u1", []string{"user"})

	for _, path := range []string{
		"/search?type=game&q=%20",     // blank q
		"/search?type=amiibo&q=zelda", // enum violation (binding)
		"/search?type=game",           // q missing (binding)
	} {
		rec := serveUnit(t, h, env, http.MethodGet, path, tok, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d", path, rec.Code)
		}
	}
}

func TestUnitGetProduct_NotFoundAndCacheHit(t *testing.T) {
	env := newAuthEnv(t)
	c := newStubCache()
	st := &stubStore{getProduct: func(_ context.Context, id string) (store.Product, error) {
		return store.Product{}, store.ErrNotFound
	}}
	h := newUnitHandlers(st, nil, nil, c)
	tok := env.token(t, "u1", []string{"user"})

	rec := serveUnit(t, h, env, http.MethodGet, "/products/33333333-3333-3333-3333-333333333333", tok, nil)
	if rec.Code != http.StatusNotFound || !bytes.Contains(rec.Body.Bytes(), []byte("product_not_found")) {
		t.Fatalf("not found: %d %s", rec.Code, rec.Body.String())
	}

	// A cached body short-circuits the store entirely.
	c.prods["44444444-4444-4444-4444-444444444444"] = []byte(`{"id":"44444444-4444-4444-4444-444444444444"}`)
	st.getProduct = func(context.Context, string) (store.Product, error) { panic("store must not be hit on a cache hit") }
	rec = serveUnit(t, h, env, http.MethodGet, "/products/44444444-4444-4444-4444-444444444444", tok, nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("44444444")) {
		t.Fatalf("cache hit: %d %s", rec.Code, rec.Body.String())
	}

	// Malformed uuid is a binding 400, not a store call.
	rec = serveUnit(t, h, env, http.MethodGet, "/products/not-a-uuid", tok, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad uuid: %d", rec.Code)
	}
}

func TestUnitGetProduct_StaleIGDBRefetchAndStaleServeOnError(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	old := time.Now().Add(-1000 * time.Hour)
	prod := store.Product{
		ID: "55555555-5555-5555-5555-555555555555", Type: "game", Name: "Chrono Trigger",
		IGDB: &store.IGDBMeta{GameID: 1011, Name: "Chrono Trigger", FetchedAt: old,
			Genres: []store.Genre{}, Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{}, Companies: []store.Company{}},
	}

	// Provider answers: the projection refreshes and is persisted.
	var setCalled bool
	st := &stubStore{
		getProduct: func(context.Context, string) (store.Product, error) { return prod, nil },
		upsertRaw:  func(context.Context, []igdb.Game, time.Time) error { return nil },
		setIGDB: func(_ context.Context, id string, m store.IGDBMeta) error {
			setCalled = true
			if m.Name != "Chrono Trigger DS" {
				t.Fatalf("refetched projection: %+v", m)
			}
			return nil
		},
	}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1011, Name: "Chrono Trigger DS"}}, nil
	}}
	h := newUnitHandlers(st, games, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodGet, "/products/"+prod.ID, tok, nil)
	if rec.Code != http.StatusOK || !setCalled || !bytes.Contains(rec.Body.Bytes(), []byte("Chrono Trigger DS")) {
		t.Fatalf("stale refetch: %d setCalled=%v %s", rec.Code, setCalled, rec.Body.String())
	}

	// Provider down: the stale copy serves.
	games.gamesByIDs = func(context.Context, []int64) ([]igdb.Game, error) { return nil, errors.New("igdb down") }
	h2 := newUnitHandlers(st, games, nil, newStubCache())
	rec = serveUnit(t, h2, env, http.MethodGet, "/products/"+prod.ID, tok, nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("Chrono Trigger")) {
		t.Fatalf("stale serve: %d %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------
// Resolve + batch prices
// ---------------------------------------------------------------

func TestResolve_GameCreatesMatchedProductIdempotently(t *testing.T) {
	s := newStack(t)

	// Chrono Trigger (fixture 1011) on SNES (19).
	p := s.resolveGame(1011, 19)
	if string(p.Type) != "game" || p.Name != "Chrono Trigger" {
		t.Fatalf("product: %+v", p)
	}
	if p.Igdb == nil || p.Igdb.GameId != 1011 || len(p.Igdb.SimilarGames) == 0 {
		t.Fatalf("igdb projection: %+v", p.Igdb)
	}
	wantDate := time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)
	if p.Igdb.FirstReleaseDate == nil || !p.Igdb.FirstReleaseDate.Equal(wantDate) {
		t.Fatalf("igdb first_release_date: %+v", p.Igdb.FirstReleaseDate)
	}
	if p.Platform == nil || p.Platform.IgdbPlatformId != 19 {
		t.Fatalf("platform: %+v", p.Platform)
	}
	if p.Pricecharting == nil || p.Pricecharting.PcProductId != 5011 || p.Pricecharting.Verified {
		t.Fatalf("auto-match must map to 5011 unverified: %+v", p.Pricecharting)
	}
	if p.Pricecharting.MatchConfidence < 0.75 || p.Pricecharting.LooseCents == nil {
		t.Fatalf("match confidence/prices: %+v", p.Pricecharting)
	}

	// Same identity resolves to the same product (found path).
	again := s.resolveGame(1011, 19)
	if again.Id != p.Id {
		t.Fatalf("resolve must be idempotent: %s vs %s", again.Id, p.Id)
	}

	// The initial snapshot landed.
	ctx := context.Background()
	n, err := s.mdb.Collection("price_snapshots").CountDocuments(ctx, map[string]any{"product_id": p.Id.String()})
	if err != nil || n != 1 {
		t.Fatalf("initial snapshot: %d, %v", n, err)
	}
	// And the raw payload is shared state for recommendations.
	raws, err := s.store.RawByIDs(ctx, []int64{1011})
	if err != nil || len(raws) != 1 {
		t.Fatalf("igdb_raw: %d, %v", len(raws), err)
	}
}

func TestResolve_UnmatchedFixtureStaysUnmatched(t *testing.T) {
	s := newStack(t)
	// Terranigma (1018, SNES) deliberately has no PriceCharting
	// fixture: the below-threshold path stores an unmatched product.
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve: %d", resp.StatusCode)
	}
	p := decodeBody[api.Product](t, resp)
	if p.Pricecharting != nil {
		t.Fatalf("Terranigma must stay unmatched: %+v", p.Pricecharting)
	}
	// Unmatched means no snapshot either.
	n, _ := s.mdb.Collection("price_snapshots").CountDocuments(context.Background(), map[string]any{"product_id": p.Id.String()})
	if n != 0 {
		t.Fatalf("unmatched products must not snapshot, got %d", n)
	}
}

func TestResolve_VariantsAreDistinctProducts(t *testing.T) {
	s := newStack(t)
	plain := s.resolveGame(1011, 19)
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19, "region": "pal",
	})
	pal := decodeBody[api.Product](t, resp)
	if pal.Id == plain.Id {
		t.Fatal("region must split identity")
	}
	if pal.Region == nil || *pal.Region != "pal" {
		t.Fatalf("region round-trip: %+v", pal.Region)
	}
}

func TestResolve_HardwareBorrowsPlatform(t *testing.T) {
	s := newStack(t)
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "console", "pc_product_id": 6001,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve hardware: %d", resp.StatusCode)
	}
	p := decodeBody[api.Product](t, resp)
	if string(p.Type) != "console" || p.Name != "Super Nintendo System" {
		t.Fatalf("hardware product: %+v", p)
	}
	if p.Pricecharting == nil || p.Pricecharting.PcProductId != 6001 || p.Pricecharting.MatchConfidence != 1.0 {
		t.Fatalf("hardware mapping: %+v", p.Pricecharting)
	}
	if p.Igdb != nil {
		t.Fatal("hardware carries no igdb subdoc")
	}
	// "Super Nintendo" (PriceCharting) borrows IGDB platform 19.
	if p.Platform == nil || p.Platform.IgdbPlatformId != 19 {
		t.Fatalf("platform borrow: %+v", p.Platform)
	}
}

func TestResolve_ErrorTaxonomy(t *testing.T) {
	s := newStack(t)
	cases := []struct {
		name string
		body map[string]any
		code int
		want string
	}{
		{"unknown game", map[string]any{"type": "game", "igdb_game_id": 999999, "platform_igdb_id": 19}, http.StatusNotFound, "unknown_game"},
		{"wrong platform", map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 130}, http.StatusBadRequest, "invalid_body"},
		{"unknown hardware", map[string]any{"type": "console", "pc_product_id": 999999}, http.StatusNotFound, "unknown_pc_product"},
		{"missing game ids", map[string]any{"type": "game"}, http.StatusBadRequest, "invalid_body"},
		{"missing pc id", map[string]any{"type": "accessory"}, http.StatusBadRequest, "invalid_body"},
		{"bad type", map[string]any{"type": "amiibo"}, http.StatusBadRequest, "invalid_body"},
	}
	for _, tc := range cases {
		resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), tc.body)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tc.code || !bytes.Contains(body, []byte(tc.want)) {
			t.Fatalf("%s: %d %s (want %d %s)", tc.name, resp.StatusCode, body, tc.code, tc.want)
		}
	}
}

func TestUnitResolve_UpstreamDown(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
		return store.Product{}, store.ErrNotFound
	}}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return nil, errors.New("igdb down")
	}}
	h := newUnitHandlers(st, games, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19})
	if rec.Code != http.StatusBadGateway || !bytes.Contains(rec.Body.Bytes(), []byte("upstream_unavailable")) {
		t.Fatalf("igdb down: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnitResolve_PriceProviderDownStillCreatesUnmatched(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	var created store.Product
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		upsertRaw: func(context.Context, []igdb.Game, time.Time) error { return nil },
		createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
			p.ID = "66666666-6666-6666-6666-666666666666"
			created = p
			return p, nil
		},
	}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1011, Name: "Chrono Trigger", Platforms: []igdb.Named{{ID: 19, Name: "Super Nintendo Entertainment System"}}}}, nil
	}}
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		return nil, errors.New("pricecharting down")
	}}
	h := newUnitHandlers(st, games, prices, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve must survive a pricing outage: %d %s", rec.Code, rec.Body.String())
	}
	if created.PriceCharting != nil {
		t.Fatal("pricing outage must store unmatched, not guess")
	}
}

// TestUnitResolve_LostRaceDoesNotDoubleSnapshot guards the fix for a
// lost create race: two concurrent resolves for the same identity
// both build a matched product, but store.CreateProduct's duplicate-
// key path converges the loser onto the winner's document (a
// different id than the loser passed in). The handler must recognize
// that convergence and skip the initial-snapshot append, since the
// winner already appended its own.
func TestUnitResolve_LostRaceDoesNotDoubleSnapshot(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1011, Name: "Chrono Trigger", Platforms: []igdb.Named{{ID: 19, Name: "Super Nintendo Entertainment System"}}}}, nil
	}}
	loose := int64(500)
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		return []pricecharting.Product{{ID: 5011, Name: "Chrono Trigger", ConsoleName: "Super Nintendo", LoosePriceCents: &loose}}, nil
	}}
	body := map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19}

	// Lost race: CreateProduct's stub plays the duplicate-key
	// convergence path by handing back a different id than it was
	// passed (the winner's document), the same shape store.go's real
	// FindProduct-on-duplicate-key fallback produces.
	var passedID string
	var snapshotCalls int
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		upsertRaw: func(context.Context, []igdb.Game, time.Time) error { return nil },
		createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
			passedID = p.ID
			p.ID = "77777777-7777-7777-7777-777777777777"
			return p, nil
		},
		appendSnapshot: func(context.Context, store.Snapshot) error {
			snapshotCalls++
			return nil
		},
	}
	h := newUnitHandlers(st, games, prices, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body.String())
	}
	if passedID == "" {
		t.Fatal("CreateProduct must be called with a pre-minted, non-empty id")
	}
	var p api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Id.String() != "77777777-7777-7777-7777-777777777777" {
		t.Fatalf("response must still serve the winner's doc: %s", p.Id)
	}
	if snapshotCalls != 0 {
		t.Fatalf("a lost race must not append a second snapshot, got %d calls", snapshotCalls)
	}

	// Won race: CreateProduct echoes the same product back (id
	// unchanged) -- the ordinary, non-converged create path.
	snapshotCalls = 0
	st2 := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		upsertRaw:     func(context.Context, []igdb.Game, time.Time) error { return nil },
		createProduct: func(_ context.Context, p store.Product) (store.Product, error) { return p, nil },
		appendSnapshot: func(context.Context, store.Snapshot) error {
			snapshotCalls++
			return nil
		},
	}
	h2 := newUnitHandlers(st2, games, prices, newStubCache())
	rec2 := serveUnit(t, h2, env, http.MethodPost, "/products/resolve", tok, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec2.Code, rec2.Body.String())
	}
	if snapshotCalls != 1 {
		t.Fatalf("winning the create race must append exactly one snapshot, got %d calls", snapshotCalls)
	}
}

func TestBatchPrices_MatrixOfKnownUnmatchedUnknown(t *testing.T) {
	s := newStack(t)
	matched := s.resolveGame(1011, 19)
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19,
	})
	unmatched := decodeBody[api.Product](t, resp)
	ghost := "99999999-9999-9999-9999-999999999999"

	resp = s.do(http.MethodPost, "/products/prices:batch", s.userToken(), map[string]any{
		"product_ids": []string{matched.Id.String(), unmatched.Id.String(), ghost},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch: %d", resp.StatusCode)
	}
	out := decodeBody[api.PricesBatchResponse](t, resp)
	if len(out.Prices) != 2 {
		t.Fatalf("unknown ids must be absent: %+v", out.Prices)
	}
	m := out.Prices[matched.Id.String()]
	if m.Unmatched || m.LooseCents == nil || m.AsOf == nil {
		t.Fatalf("matched prices: %+v", m)
	}
	u := out.Prices[unmatched.Id.String()]
	if !u.Unmatched || u.LooseCents != nil {
		t.Fatalf("unmatched prices: %+v", u)
	}
	if _, ok := out.Prices[ghost]; ok {
		t.Fatal("ghost id leaked into the response")
	}
}

func TestUnitBatchPrices_CapAndBadBody(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	h := newUnitHandlers(nil, nil, nil, newStubCache())

	ids := make([]string, 501)
	for i := range ids {
		ids[i] = "11111111-1111-1111-1111-111111111111"
	}
	rec := serveUnit(t, h, env, http.MethodPost, "/products/prices:batch", tok, map[string]any{"product_ids": ids})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cap: %d", rec.Code)
	}

	req := httptest.NewRequest(http.MethodPost, "/products/prices:batch", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec2 := httptest.NewRecorder()
	NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil }).ServeHTTP(rec2, req)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", rec2.Code)
	}
}

// ---------------------------------------------------------------
// Recommendations
// ---------------------------------------------------------------

func TestRecommendations_EndToEndOverFixtures(t *testing.T) {
	s := newStack(t)
	// A Zelda/Souls library: candidates must come from similar_games
	// edges, exclude owned ids, and carry display metadata fetched
	// into igdb_raw on demand.
	body := map[string]any{"library": []map[string]any{
		{"igdb_game_id": 1001, "rating": 10},        // OoT, loved
		{"igdb_game_id": 1042},                      // Dark Souls
		{"igdb_game_id": 1012, "status": "dropped"}, // Chrono Cross, dropped
	}}
	resp := s.do(http.MethodPost, "/recommendations:score", s.userToken(), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("score: %d", resp.StatusCode)
	}
	out := decodeBody[api.ScoreResponse](t, resp)
	if out.Degraded {
		t.Fatal("stub providers must not degrade")
	}
	if len(out.Recommendations) == 0 {
		t.Fatal("no recommendations")
	}
	seen := map[int64]float64{}
	var linkToPast *api.Recommendation
	for i, rec := range out.Recommendations {
		if rec.IgdbGameId == 1001 || rec.IgdbGameId == 1042 || rec.IgdbGameId == 1012 {
			t.Fatalf("owned id recommended: %d", rec.IgdbGameId)
		}
		if rec.Name == "" || len(rec.Genres) == 0 {
			t.Fatalf("display metadata missing: %+v", rec)
		}
		if i > 0 && out.Recommendations[i-1].Score < rec.Score {
			t.Fatal("not sorted by score desc")
		}
		seen[rec.IgdbGameId] = rec.Score
		if rec.IgdbGameId == 1002 {
			linkToPast = &rec
		}
	}
	// OoT (weight 2.0) links 1002/1003/1004/1035/1037: at least one of
	// its edges must outrank anything reachable only through the
	// dropped Chrono Cross (weight 0.5).
	if _, ok := seen[1002]; !ok {
		t.Fatalf("expected a strong Zelda edge in %v", seen)
	}
	wantDate := time.Date(1991, time.November, 21, 0, 0, 0, 0, time.UTC)
	if linkToPast == nil || linkToPast.FirstReleaseDate == nil || !linkToPast.FirstReleaseDate.Equal(wantDate) {
		t.Fatalf("recommendation first_release_date: %+v", linkToPast)
	}
	// Candidate metadata was populated backwards into igdb_raw.
	raws, err := s.store.RawByIDs(context.Background(), []int64{1002})
	if err != nil || len(raws) != 1 {
		t.Fatalf("igdb_raw candidate population: %d, %v", len(raws), err)
	}
}

func TestRecommendations_EmptyLibrary(t *testing.T) {
	s := newStack(t)
	resp := s.do(http.MethodPost, "/recommendations:score", s.userToken(), map[string]any{"library": []any{}})
	out := decodeBody[api.ScoreResponse](t, resp)
	if out.Degraded || len(out.Recommendations) != 0 {
		t.Fatalf("empty library: %+v", out)
	}
}

func TestRecommendations_SparseLibraryUsesGenreFallback(t *testing.T) {
	s := newStack(t)
	// Pokemon Emerald's only edge is FireRed: the pool is far below the
	// limit, so the genre profile (RPG) must top it up with well-rated
	// RPG fixtures the user does not own.
	body := map[string]any{"library": []map[string]any{{"igdb_game_id": 1020, "rating": 9}}}
	resp := s.do(http.MethodPost, "/recommendations:score", s.userToken(), body)
	out := decodeBody[api.ScoreResponse](t, resp)
	if len(out.Recommendations) < 5 {
		t.Fatalf("fallback did not top up: %d", len(out.Recommendations))
	}
	for _, rec := range out.Recommendations {
		if rec.IgdbGameId == 1020 {
			t.Fatal("owned id recommended by fallback")
		}
	}
}

// TestUnitRecommendations_DegradedOnMetadataFetchFailure covers the
// owned-game metadata fetch failing outright: igdb_raw has nothing for
// the owned id, and the provider (GamesByIDs) is down too, so the
// first ensureRaw call degrades before any candidate or genre logic
// ever runs.
func TestUnitRecommendations_DegradedOnMetadataFetchFailure(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
	}
	games := &stubGames{
		gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) { return nil, errors.New("igdb down") },
	}
	h := newUnitHandlers(st, games, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/recommendations:score", tok,
		map[string]any{"library": []map[string]any{{"igdb_game_id": 1001}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("degraded scoring must still answer: %d %s", rec.Code, rec.Body.String())
	}
	var out api.ScoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Degraded || len(out.Recommendations) != 0 {
		t.Fatalf("want degraded empty answer: %+v", out)
	}
}

// TestUnitRecommendations_DegradedOnGenreFallbackFailure covers the
// other degraded trigger: the owned game's metadata fetch succeeds (via
// igdb_raw, no provider call needed) with a genre but no similar_games
// edges, so the edge-derived candidate pool stays empty (below limit)
// and the genre-profile fallback must run -- where PopularGames then
// fails, which is the branch this test exists to exercise.
func TestUnitRecommendations_DegradedOnGenreFallbackFailure(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) {
			// Owned, with a genre but an empty similar_games list: no
			// edges means CandidateIDs stays empty, forcing the
			// genre-profile fallback below.
			return []store.RawGame{{
				GameID: 1001,
				Game:   igdb.Game{ID: 1001, Name: "Owned", Genres: []igdb.Named{{ID: 12, Name: "Role-playing (RPG)"}}},
			}}, nil
		},
	}
	games := &stubGames{
		popularGames: func(context.Context, []int64, []int64, int) ([]igdb.Game, error) {
			return nil, errors.New("igdb down")
		},
	}
	h := newUnitHandlers(st, games, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/recommendations:score", tok,
		map[string]any{"library": []map[string]any{{"igdb_game_id": 1001}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("degraded scoring must still answer: %d %s", rec.Code, rec.Body.String())
	}
	var out api.ScoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Degraded || len(out.Recommendations) != 0 {
		t.Fatalf("want a degraded answer from a failed genre fallback: %+v", out)
	}
}

func TestUnitRecommendations_LimitClamped(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	// 60 candidates via one owned game; limit asks for 999 -> clamps to 50.
	similar := make([]int64, 60)
	for i := range similar {
		similar[i] = int64(2000 + i)
	}
	rawOwned := store.RawGame{GameID: 1, Game: igdb.Game{ID: 1, Name: "Owned", SimilarGames: similar}}
	st := &stubStore{
		rawByIDs: func(_ context.Context, ids []int64) ([]store.RawGame, error) {
			out := make([]store.RawGame, 0, len(ids))
			for _, id := range ids {
				if id == 1 {
					out = append(out, rawOwned)
					continue
				}
				out = append(out, store.RawGame{GameID: id, Game: igdb.Game{ID: id, Name: "Candidate", Genres: []igdb.Named{{ID: 12, Name: "Role-playing (RPG)"}}}})
			}
			return out, nil
		},
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/recommendations:score", tok,
		map[string]any{"library": []map[string]any{{"igdb_game_id": 1}}, "limit": 999})
	var out api.ScoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Recommendations) != 50 {
		t.Fatalf("limit clamp: %d", len(out.Recommendations))
	}
}

// ---------------------------------------------------------------
// Refresh runner + admin endpoints
// ---------------------------------------------------------------

// waitFor polls until check passes (the refresh walk is detached).
func waitFor(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}

// doInternal drives the CronJob path: no JWT, the internal token
// header instead.
func (s *stack) doInternal(token string) *http.Response {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.srv.URL+"/internal/refresh", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp
}

// serveInternal is the unit-layer equivalent of doInternal.
func serveInternal(t *testing.T, h *Handlers, env *authEnv, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/refresh", nil)
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	rec := httptest.NewRecorder()
	router := NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil })
	router.ServeHTTP(rec, req)
	return rec
}

func TestRefresh_InternalWalksCatalogAndSnapshots(t *testing.T) {
	s := newStack(t)
	matched := s.resolveGame(1011, 19)
	s.resolveGame(1013, 7)
	// One unmatched product: the walk must skip it.
	_ = s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19,
	}).Body.Close()

	resp := s.doInternal(testInternalToken) // no JWT: the CronJob path
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("internal refresh: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	ctx := context.Background()
	waitFor(t, 10*time.Second, func() bool {
		// Each matched product got its resolve-time snapshot plus one
		// walk snapshot; Terranigma got none.
		n, err := s.mdb.Collection("price_snapshots").CountDocuments(ctx, map[string]any{})
		return err == nil && n == 4
	})
	got, err := s.store.GetProduct(ctx, matched.Id.String())
	if err != nil || got.PriceCharting == nil {
		t.Fatalf("walked product: %v", err)
	}
	if got.PriceCharting.AsOf.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("as_of not refreshed: %v", got.PriceCharting.AsOf)
	}
}

func TestRefresh_AdminRBACAndConflict(t *testing.T) {
	s := newStack(t)

	// Non-admin: 403 with the forbidden code.
	resp := s.do(http.MethodPost, "/admin/refresh", s.userToken(), nil)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("forbidden")) {
		t.Fatalf("non-admin: %d %s", resp.StatusCode, body)
	}

	// Admin: accepted.
	resp = s.do(http.MethodPost, "/admin/refresh", s.adminToken(), nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("admin: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	waitFor(t, 10*time.Second, func() bool { return !s.h.refreshing.Load() })
}

func TestUnitInternalRefresh_TokenGuard(t *testing.T) {
	env := newAuthEnv(t)
	h := New(&stubStore{listPriced: func(context.Context) ([]store.Product, error) { return nil, nil }},
		nil, nil, newStubCache(), Options{
			// An A/B pair mid-rotation: both must be accepted.
			InternalRefreshSecrets: []string{"new-token", "old-token"},
			Logger:                 slog.New(slog.DiscardHandler),
		})

	rec := serveInternal(t, h, env, "")
	if rec.Code != http.StatusUnauthorized || !bytes.Contains(rec.Body.Bytes(), []byte("invalid_internal_token")) {
		t.Fatalf("missing token: %d %s", rec.Code, rec.Body.String())
	}
	rec = serveInternal(t, h, env, "guessed-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", rec.Code)
	}
	for _, tok := range []string{"new-token", "old-token"} {
		rec = serveInternal(t, h, env, tok)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("accepted token %q: %d %s", tok, rec.Code, rec.Body.String())
		}
		waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
	}
}

func TestUnitRefresh_ConflictWhileRunning(t *testing.T) {
	env := newAuthEnv(t)
	release := make(chan struct{})
	started := make(chan struct{})
	st := &stubStore{listPriced: func(context.Context) ([]store.Product, error) {
		close(started)
		<-release
		return nil, nil
	}}
	h := newUnitHandlers(st, nil, nil, newStubCache())

	rec := serveInternal(t, h, env, testInternalToken)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first trigger: %d", rec.Code)
	}
	<-started
	rec = serveInternal(t, h, env, testInternalToken)
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("refresh_in_progress")) {
		t.Fatalf("concurrent trigger: %d %s", rec.Code, rec.Body.String())
	}
	close(release)
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })

	// The guard resets: a third trigger is accepted again.
	st.listPriced = func(context.Context) ([]store.Product, error) { return nil, nil }
	rec = serveInternal(t, h, env, testInternalToken)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post-walk trigger: %d", rec.Code)
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
}

func TestUnitRefresh_WalkSurvivesPerProductFailures(t *testing.T) {
	env := newAuthEnv(t)
	loose := int64(1000)
	prods := []store.Product{
		{ID: "aaaaaaaa-0000-0000-0000-000000000001", PriceCharting: &store.PCMeta{PCProductID: 1}},
		{ID: "aaaaaaaa-0000-0000-0000-000000000002", PriceCharting: &store.PCMeta{PCProductID: 2}},
	}
	var snaps int
	st := &stubStore{
		listPriced: func(context.Context) ([]store.Product, error) { return prods, nil },
		setCurrentPrices: func(_ context.Context, id string, _ store.PriceQuote, _ time.Time) error {
			return nil
		},
		appendSnapshot: func(context.Context, store.Snapshot) error { snaps++; return nil },
	}
	prices := &stubPrices{product: func(_ context.Context, id int64) (pricecharting.Product, error) {
		if id == 1 {
			return pricecharting.Product{}, errors.New("flaky provider")
		}
		return pricecharting.Product{ID: id, Name: "P", ConsoleName: "C", LoosePriceCents: &loose}, nil
	}}
	h := newUnitHandlers(st, nil, prices, newStubCache())

	rec := serveInternal(t, h, env, testInternalToken)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trigger: %d", rec.Code)
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
	if snaps != 1 {
		t.Fatalf("the healthy product must still snapshot: %d", snaps)
	}
}

func TestUnitRefresh_WalkPanicIsContained(t *testing.T) {
	env := newAuthEnv(t)
	st := &stubStore{listPriced: func(context.Context) ([]store.Product, error) {
		panic("boom")
	}}
	h := newUnitHandlers(st, nil, nil, newStubCache())

	rec := serveInternal(t, h, env, testInternalToken)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trigger before the panicking walk: %d", rec.Code)
	}
	// If the panic escaped the goroutine, the whole test binary would
	// already be dead here; reaching this line at all is part of the
	// proof.
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })

	// The guard reset after the panic: a second trigger is accepted
	// again, not 409 (a leaked guard would answer 409 forever).
	st.listPriced = func(context.Context) ([]store.Product, error) { return nil, nil }
	rec = serveInternal(t, h, env, testInternalToken)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post-panic trigger: %d", rec.Code)
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
}

func TestAdminMapping_CorrectVerifyClearAndErrors(t *testing.T) {
	s := newStack(t)
	// Terranigma resolves unmatched; the admin pins it to a mapping.
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19,
	})
	p := decodeBody[api.Product](t, resp)
	id := p.Id.String()

	// Non-admin: 403.
	resp = s.do(http.MethodPut, "/admin/products/"+id+"/pricecharting", s.userToken(), map[string]any{"pc_product_id": 5017})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin remap: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// Admin remap to EarthBound's product id: verified, priced,
	// snapshotted, visible immediately.
	resp = s.do(http.MethodPut, "/admin/products/"+id+"/pricecharting", s.adminToken(), map[string]any{"pc_product_id": 5017})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("remap: %d %s", resp.StatusCode, body)
	}
	mapped := decodeBody[api.Product](t, resp)
	if mapped.Pricecharting == nil || mapped.Pricecharting.PcProductId != 5017 ||
		!mapped.Pricecharting.Verified || mapped.Pricecharting.MatchConfidence != 1.0 ||
		mapped.Pricecharting.LooseCents == nil {
		t.Fatalf("mapping: %+v", mapped.Pricecharting)
	}
	n, _ := s.mdb.Collection("price_snapshots").CountDocuments(context.Background(), map[string]any{"product_id": id})
	if n != 1 {
		t.Fatalf("mapping snapshot: %d", n)
	}
	// The read path serves the correction immediately (cache dropped).
	resp = s.do(http.MethodGet, "/products/"+id, s.userToken(), nil)
	fresh := decodeBody[api.Product](t, resp)
	if fresh.Pricecharting == nil || fresh.Pricecharting.PcProductId != 5017 {
		t.Fatalf("read-after-remap: %+v", fresh.Pricecharting)
	}

	// Clearing makes it unmatched again.
	resp = s.do(http.MethodPut, "/admin/products/"+id+"/pricecharting", s.adminToken(), map[string]any{"pc_product_id": nil})
	cleared := decodeBody[api.Product](t, resp)
	if cleared.Pricecharting != nil {
		t.Fatalf("clear: %+v", cleared.Pricecharting)
	}

	// Error taxonomy.
	resp = s.do(http.MethodPut, "/admin/products/"+id+"/pricecharting", s.adminToken(), map[string]any{"pc_product_id": 999999})
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !bytes.Contains(body, []byte("unknown_pc_product")) {
		t.Fatalf("unknown mapping: %d %s", resp.StatusCode, body)
	}
	ghost := "99999999-9999-9999-9999-999999999999"
	resp = s.do(http.MethodPut, "/admin/products/"+ghost+"/pricecharting", s.adminToken(), map[string]any{"pc_product_id": 5017})
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !bytes.Contains(body, []byte("product_not_found")) {
		t.Fatalf("ghost product: %d %s", resp.StatusCode, body)
	}
}
