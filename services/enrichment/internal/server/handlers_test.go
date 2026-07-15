package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/levonn-dev/vg-collect/libs/go/valkeykit"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/cache"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/db"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/fx"
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
	findProduct               func(ctx context.Context, key store.ProductKey) (store.Product, error)
	createProduct             func(ctx context.Context, p store.Product) (store.Product, error)
	getProduct                func(ctx context.Context, id string) (store.Product, error)
	setIGDB                   func(ctx context.Context, id string, m store.IGDBMeta) error
	setPriceCharting          func(ctx context.Context, id string, m *store.PCMeta) error
	setPriceChartingIfMissing func(ctx context.Context, id string, m *store.PCMeta) (bool, error)
	setCurrentPrices          func(ctx context.Context, id string, q store.PriceQuote, asOf time.Time) error
	listPriced                func(ctx context.Context) ([]store.Product, error)
	productsByIDs             func(ctx context.Context, ids []string) ([]store.Product, error)
	searchByName              func(ctx context.Context, q string, limit int) ([]store.Product, error)
	upsertRaw                 func(ctx context.Context, games []igdb.Game, fetchedAt time.Time) error
	rawByIDs                  func(ctx context.Context, ids []int64) ([]store.RawGame, error)
	upsertPlatforms           func(ctx context.Context, ps []igdb.Platform, fetchedAt time.Time) error
	listPlatforms             func(ctx context.Context) ([]store.CatalogPlatform, error)
	platformsFetchedAt        func(ctx context.Context) (time.Time, error)
	appendSnapshot            func(ctx context.Context, s store.Snapshot) error
	snapshotsSince            func(ctx context.Context, ids []string, since time.Time) (map[string][]store.Snapshot, error)
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

func (s *stubStore) SetPriceChartingIfMissing(ctx context.Context, id string, m *store.PCMeta) (bool, error) {
	if s.setPriceChartingIfMissing == nil {
		panic("unexpected SetPriceChartingIfMissing")
	}
	return s.setPriceChartingIfMissing(ctx, id, m)
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

func (s *stubStore) ListPlatforms(ctx context.Context) ([]store.CatalogPlatform, error) {
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

func (s *stubStore) SnapshotsSince(ctx context.Context, ids []string, since time.Time) (map[string][]store.Snapshot, error) {
	if s.snapshotsSince == nil {
		panic("unexpected SnapshotsSince")
	}
	return s.snapshotsSince(ctx, ids, since)
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

// stubFX implements server.FXProvider via a function field.
type stubFX struct {
	latest func(ctx context.Context) (fx.Rates, error)
}

var _ FXProvider = (*stubFX)(nil)

func (s *stubFX) Latest(ctx context.Context) (fx.Rates, error) {
	if s.latest == nil {
		panic("unexpected Latest")
	}
	return s.latest(ctx)
}

// newUnitHandlers builds Handlers over stubs with fast defaults. Every
// caller here is indifferent to FX, so the fx collaborator is always
// the zero-configured stub; tests that care about FX behavior build
// Handlers directly (see doAuthedFxRequest) so they can supply their
// own configured stub instead.
func newUnitHandlers(st Store, games GameProvider, prices PriceProvider, c Cache) *Handlers {
	return New(st, games, prices, &stubFX{}, c, Options{
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

// doAuthedFxRequest builds Handlers the same way newUnitHandlers does
// (same TTLs, internal secret and discard logger) but with rates as
// the fx collaborator instead of the default stub, mints the usual
// test JWT, and serves an authed GET /fx/latest through NewRouter --
// mirroring TestUnitGetProduct_NotFoundAndCacheHit's harness (same
// validator, logger and ready func), the nearest existing authed GET
// test in this file.
func doAuthedFxRequest(t *testing.T, rates FXProvider) *httptest.ResponseRecorder {
	t.Helper()
	env := newAuthEnv(t)
	h := New(nil, nil, nil, rates, newStubCache(), Options{
		SearchCacheTTL:         time.Hour,
		ProductCacheTTL:        time.Minute,
		IGDBRefreshAfter:       720 * time.Hour,
		InternalRefreshSecrets: []string{testInternalToken},
		Logger:                 slog.New(slog.DiscardHandler),
	})
	tok := env.token(t, "u1", []string{"user"})
	return serveUnit(t, h, env, http.MethodGet, "/fx/latest", tok, nil)
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
	rates, err := fx.NewStub()
	if err != nil {
		t.Fatal(err)
	}
	env := newAuthEnv(t)
	h := New(st, games, prices, rates, cache.New(rdb), Options{
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

	// The accepted-kinds message must list all three wire kinds.
	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=amiibo&q=zelda", tok, nil)
	if !strings.Contains(rec.Body.String(), "type must be game, hardware or pc_listing") {
		t.Fatalf("accepted-kinds message stale: %s", rec.Body.String())
	}
}

func TestSearch_PCListingsAllCategoriesWithPrices(t *testing.T) {
	s := newStack(t)
	resp := s.do(http.MethodGet, "/search?type=pc_listing&q=super+mario+64", s.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	out := decodeBody[api.SearchResults](t, resp)
	if out.Degraded {
		t.Fatal("stub provider must not degrade")
	}
	if len(out.Results) == 0 {
		t.Fatal("want the fixture game listing(s)")
	}
	for _, r := range out.Results {
		if string(r.Type) != "pc_listing" {
			t.Fatalf("result type %s, want pc_listing", r.Type)
		}
		if r.PcProductId == nil || r.ConsoleName == nil {
			t.Fatal("pc_listing results carry the listing id and console")
		}
		if r.LooseCents == nil || r.CibCents == nil || r.NewCents == nil {
			t.Fatal("stub listings always price; results must pass prices through")
		}
	}
	// Game listings (no hardware category) must NOT be filtered out:
	// the fixtures' Super Mario 64 game row has no genre.
	found := false
	for _, r := range out.Results {
		if *r.PcProductId == 5005 {
			found = true
		}
	}
	if !found {
		t.Fatal("game listing 5005 missing: category filter must not apply to pc_listing search")
	}
}

func TestUnitSearch_PCListingsDegradedFallsBackToMappings(t *testing.T) {
	env := newAuthEnv(t)
	st := &stubStore{}
	prices := &stubPrices{}
	prices.search = func(ctx context.Context, q string) ([]pricecharting.Product, error) {
		return nil, errors.New("provider down")
	}
	loose := int64(1100)
	st.searchByName = func(ctx context.Context, q string, limit int) ([]store.Product, error) {
		return []store.Product{{
			ID: "p1", Type: "game", Name: "Super Mario 64",
			PriceCharting: &store.PCMeta{
				PCProductID: 5005, PCName: "Super Mario 64", ConsoleName: "Nintendo 64",
				Current: store.PriceQuote{LooseCents: &loose},
			},
		}, {
			ID: "p2", Type: "game", Name: "Unmatched Game", // no PCMeta: not a listing
		}}, nil
	}
	h := newUnitHandlers(st, &stubGames{}, prices, newStubCache())
	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=pc_listing&q=mario", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var out api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Degraded {
		t.Fatal("provider down + cold cache must flag degraded")
	}
	if len(out.Results) != 1 {
		t.Fatalf("only mapped products can answer a degraded pc_listing search; got %d results", len(out.Results))
	}
	r := out.Results[0]
	if string(r.Type) != "pc_listing" || *r.PcProductId != 5005 || r.Name != "Super Mario 64" ||
		r.LooseCents == nil || *r.LooseCents != 1100 {
		t.Fatalf("degraded result must map the stored PCMeta, got %+v", r)
	}
}

// TestUnitSearch_PCListingsDegradedDedupesSharedPCProductID pins the
// degraded local-fallback dedupe: a resolved game product that
// auto-matched to a PriceCharting listing, and a separate pc_listing
// anchor product created straight off that same listing id, are two
// distinct local documents with no identity tying them together -
// both can match the query text and both carry the same
// pc_product_id, so the fallback must collapse them to one row.
func TestUnitSearch_PCListingsDegradedDedupesSharedPCProductID(t *testing.T) {
	env := newAuthEnv(t)
	st := &stubStore{}
	prices := &stubPrices{}
	prices.search = func(ctx context.Context, q string) ([]pricecharting.Product, error) {
		return nil, errors.New("provider down")
	}
	loose := int64(1100)
	st.searchByName = func(ctx context.Context, q string, limit int) ([]store.Product, error) {
		return []store.Product{
			{
				ID: "game-1", Type: "game", Name: "Super Mario 64",
				PriceCharting: &store.PCMeta{
					PCProductID: 5005, PCName: "Super Mario 64", ConsoleName: "Nintendo 64",
					Current: store.PriceQuote{LooseCents: &loose},
				},
			},
			{
				ID: "listing-1", Type: "pc_listing", Name: "Super Mario 64",
				PriceCharting: &store.PCMeta{
					PCProductID: 5005, PCName: "Super Mario 64", ConsoleName: "Nintendo 64",
					Current: store.PriceQuote{LooseCents: &loose},
				},
			},
		}, nil
	}
	h := newUnitHandlers(st, &stubGames{}, prices, newStubCache())
	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=pc_listing&q=mario", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var out api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("degraded pc_listing results must dedupe by pc_product_id: got %d, %+v", len(out.Results), out.Results)
	}
	if *out.Results[0].PcProductId != 5005 {
		t.Fatalf("unexpected surviving row: %+v", out.Results[0])
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

func TestUnitGetProduct_FreshIGDBSkipsProviderRefetch(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	fresh := time.Now().Add(-1 * time.Hour) // well inside the 720h window
	prod := store.Product{
		ID: "66666666-6666-6666-6666-666666666666", Type: "game", Name: "Chrono Trigger",
		IGDB: &store.IGDBMeta{GameID: 1011, Name: "Chrono Trigger", FetchedAt: fresh,
			Genres: []store.Genre{}, Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{}, Companies: []store.Company{}},
	}
	st := &stubStore{getProduct: func(context.Context, string) (store.Product, error) { return prod, nil }}
	// gamesByIDs, upsertRaw and setIGDB are all left nil: a fresh
	// product must not touch the provider or the store's write paths,
	// and every stub panics loudly if called unexpectedly.
	games := &stubGames{}
	h := newUnitHandlers(st, games, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/products/"+prod.ID, tok, nil)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte("Chrono Trigger")) {
		t.Fatalf("fresh serve: %d %s", rec.Code, rec.Body.String())
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

func TestResolve_GameManualMatchMintsExactAnchor(t *testing.T) {
	s := newStack(t)
	// 6001 is the Super Nintendo System fixture - a listing the game
	// auto-match would never pick for Chrono Trigger (it maps to 5011),
	// so the stored mapping proves the manual path ran.
	body := map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19, "pc_product_id": 6001}
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve: %d", resp.StatusCode)
	}
	p := decodeBody[api.Product](t, resp)
	if p.Pricecharting == nil || p.Pricecharting.PcProductId != 6001 {
		t.Fatalf("manual match must win over auto-match: %+v", p.Pricecharting)
	}
	if p.Pricecharting.MatchConfidence != 1.0 || p.Pricecharting.Verified {
		t.Fatalf("exact but unverified: %+v", p.Pricecharting)
	}
	if p.Igdb == nil || p.Igdb.GameId != 1011 {
		t.Fatalf("still a full game product: %+v", p.Igdb)
	}
	n, err := s.mdb.Collection("price_snapshots").CountDocuments(context.Background(), map[string]any{"product_id": p.Id.String()})
	if err != nil || n != 1 {
		t.Fatalf("initial snapshot: %d, %v", n, err)
	}

	// Idempotent: the same manual resolve converges on the same doc
	// and appends nothing.
	again := decodeBody[api.Product](t, s.do(http.MethodPost, "/products/resolve", s.userToken(), body))
	if again.Id != p.Id {
		t.Fatalf("must converge: %s vs %s", again.Id, p.Id)
	}
	n, _ = s.mdb.Collection("price_snapshots").CountDocuments(context.Background(), map[string]any{"product_id": p.Id.String()})
	if n != 1 {
		t.Fatalf("repeat resolve must not snapshot again, got %d", n)
	}
}

func TestResolve_GameManualMatchFillsMissingAnchor(t *testing.T) {
	s := newStack(t)
	// Terranigma (1018, SNES) auto-misses: stored unmatched, a dead end
	// the walk skips.
	first := decodeBody[api.Product](t, s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19,
	}))
	if first.Pricecharting != nil {
		t.Fatalf("precondition: Terranigma must start unmatched: %+v", first.Pricecharting)
	}

	// Same identity with a manual match: the hole fills, once.
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19, "pc_product_id": 6001,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fill resolve: %d", resp.StatusCode)
	}
	filled := decodeBody[api.Product](t, resp)
	if filled.Id != first.Id {
		t.Fatalf("fill must converge on the same product: %s vs %s", filled.Id, first.Id)
	}
	if filled.Pricecharting == nil || filled.Pricecharting.PcProductId != 6001 ||
		filled.Pricecharting.MatchConfidence != 1.0 || filled.Pricecharting.Verified {
		t.Fatalf("filled mapping: %+v", filled.Pricecharting)
	}
	n, err := s.mdb.Collection("price_snapshots").CountDocuments(context.Background(), map[string]any{"product_id": first.Id.String()})
	if err != nil || n != 1 {
		t.Fatalf("fill must snapshot once: %d, %v", n, err)
	}

	// The fill is immediately visible on the read path (cache included).
	got := decodeBody[api.Product](t, s.do(http.MethodGet, "/products/"+first.Id.String(), s.userToken(), nil))
	if got.Pricecharting == nil || got.Pricecharting.PcProductId != 6001 {
		t.Fatalf("read-after-fill: %+v", got.Pricecharting)
	}
}

func TestResolve_GameManualMatchNeverOverwrites(t *testing.T) {
	s := newStack(t)
	p := s.resolveGame(1011, 19) // auto-matches to 5011 with one snapshot
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19, "pc_product_id": 6001,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve: %d", resp.StatusCode)
	}
	same := decodeBody[api.Product](t, resp)
	if same.Id != p.Id {
		t.Fatalf("must converge: %s vs %s", same.Id, p.Id)
	}
	if same.Pricecharting == nil || same.Pricecharting.PcProductId != 5011 {
		t.Fatalf("existing mapping must survive a manual match: %+v", same.Pricecharting)
	}
	n, _ := s.mdb.Collection("price_snapshots").CountDocuments(context.Background(), map[string]any{"product_id": p.Id.String()})
	if n != 1 {
		t.Fatalf("no new snapshot on an untouched mapping, got %d", n)
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
		platformsFetchedAt: func(context.Context) (time.Time, error) { return time.Now(), nil },
		listPlatforms: func(context.Context) ([]store.CatalogPlatform, error) {
			return []store.CatalogPlatform{{ID: 19, Name: "Super Nintendo Entertainment System", LogoURL: "https://images.igdb.com/igdb/image/upload/t_logo_med/pl4k.jpg"}}, nil
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
	// The platform ref picks up the catalog logo at create.
	if created.Platform == nil || created.Platform.LogoURL != "https://images.igdb.com/igdb/image/upload/t_logo_med/pl4k.jpg" {
		t.Fatalf("platform logo must ride the created product: %+v", created.Platform)
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
	catalogStub := func(context.Context) ([]store.CatalogPlatform, error) {
		return []store.CatalogPlatform{{ID: 19, Name: "Super Nintendo Entertainment System"}}, nil
	}
	fetchedAtStub := func(context.Context) (time.Time, error) { return time.Now(), nil }
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
		platformsFetchedAt: fetchedAtStub,
		listPlatforms:      catalogStub,
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
		platformsFetchedAt: fetchedAtStub,
		listPlatforms:      catalogStub,
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

func TestUnitResolve_GameManualMatchMintsExactAnchor(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1011, Name: "Chrono Trigger", Platforms: []igdb.Named{{ID: 19, Name: "Super Nintendo Entertainment System"}}}}, nil
	}}
	// search is deliberately unset: if the manual path ever consulted
	// auto-match, the stub would panic.
	loose := int64(12500)
	prices := &stubPrices{product: func(_ context.Context, id int64) (pricecharting.Product, error) {
		if id != 7042 {
			t.Errorf("manual match must fetch the chosen listing, got %d", id)
		}
		return pricecharting.Product{ID: 7042, Name: "Chrono Trigger [PAL]", ConsoleName: "PAL Super Nintendo", LoosePriceCents: &loose}, nil
	}}
	var created store.Product
	var snapshots int
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		upsertRaw:     func(context.Context, []igdb.Game, time.Time) error { return nil },
		createProduct: func(_ context.Context, p store.Product) (store.Product, error) { created = p; return p, nil },
		appendSnapshot: func(context.Context, store.Snapshot) error {
			snapshots++
			return nil
		},
		platformsFetchedAt: func(context.Context) (time.Time, error) { return time.Now(), nil },
		listPlatforms: func(context.Context) ([]store.CatalogPlatform, error) {
			return []store.CatalogPlatform{{ID: 19, Name: "Super Nintendo Entertainment System"}}, nil
		},
	}
	h := newUnitHandlers(st, games, prices, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19, "pc_product_id": 7042})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body.String())
	}
	if created.PriceCharting == nil || created.PriceCharting.PCProductID != 7042 {
		t.Fatalf("manual match must map the product: %+v", created.PriceCharting)
	}
	if created.PriceCharting.MatchConfidence != 1.0 || created.PriceCharting.Verified {
		t.Fatalf("manual match is exact but unverified: %+v", created.PriceCharting)
	}
	if created.IGDB == nil || created.IGDB.GameID != 1011 {
		t.Fatalf("still a full game product: %+v", created.IGDB)
	}
	if snapshots != 1 {
		t.Fatalf("manual mint must append the initial snapshot, got %d", snapshots)
	}
}

func TestUnitResolve_GameManualMatchErrors(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1011, Name: "Chrono Trigger", Platforms: []igdb.Named{{ID: 19, Name: "Super Nintendo Entertainment System"}}}}, nil
	}}
	notFound := func(context.Context, store.ProductKey) (store.Product, error) {
		return store.Product{}, store.ErrNotFound
	}
	catalogOK := func(context.Context) ([]store.CatalogPlatform, error) {
		return []store.CatalogPlatform{{ID: 19, Name: "Super Nintendo Entertainment System"}}, nil
	}
	fetchedOK := func(context.Context) (time.Time, error) { return time.Now(), nil }
	body := map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19, "pc_product_id": 7042}

	// Unknown listing -> 404 unknown_pc_product.
	st := &stubStore{findProduct: notFound, upsertRaw: func(context.Context, []igdb.Game, time.Time) error { return nil },
		platformsFetchedAt: fetchedOK, listPlatforms: catalogOK}
	prices := &stubPrices{product: func(context.Context, int64) (pricecharting.Product, error) {
		return pricecharting.Product{}, pricecharting.ErrNotFound
	}}
	rec := serveUnit(t, newUnitHandlers(st, games, prices, newStubCache()), env, http.MethodPost, "/products/resolve", tok, body)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown_pc_product") {
		t.Fatalf("unknown listing: %d %s", rec.Code, rec.Body.String())
	}

	// Provider down -> 502 upstream_unavailable.
	prices.product = func(context.Context, int64) (pricecharting.Product, error) {
		return pricecharting.Product{}, errors.New("boom")
	}
	rec = serveUnit(t, newUnitHandlers(st, games, prices, newStubCache()), env, http.MethodPost, "/products/resolve", tok, body)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("provider down: %d %s", rec.Code, rec.Body.String())
	}

	// Bogus game id + manual match -> 404 unknown_game: the IGDB fetch
	// runs first, and the listing is never fetched (unset = panic).
	noGames := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) { return nil, nil }}
	rec = serveUnit(t, newUnitHandlers(&stubStore{findProduct: notFound}, noGames, &stubPrices{}, newStubCache()),
		env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 999999, "platform_igdb_id": 19, "pc_product_id": 7042})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown_game") {
		t.Fatalf("unknown game must win: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnitResolve_GameManualMatchFillsAndGatesSnapshot(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	loose := int64(9800)
	prices := &stubPrices{product: func(_ context.Context, id int64) (pricecharting.Product, error) {
		return pricecharting.Product{ID: id, Name: "Terranigma [PAL]", ConsoleName: "PAL Super Nintendo", LoosePriceCents: &loose}, nil
	}}
	unmatched := store.Product{ID: "11111111-1111-1111-1111-111111111111", Type: "game", Name: "Terranigma"}
	body := map[string]any{"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19, "pc_product_id": 7042}

	// Fill lands: mapping set, snapshot appended, filled doc served.
	var snapshots int
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) { return unmatched, nil },
		setPriceChartingIfMissing: func(_ context.Context, id string, m *store.PCMeta) (bool, error) {
			if id != unmatched.ID || m == nil || m.PCProductID != 7042 || m.Verified {
				t.Errorf("fill args: id=%s meta=%+v", id, m)
			}
			return true, nil
		},
		appendSnapshot: func(_ context.Context, snap store.Snapshot) error {
			if snap.ProductID != unmatched.ID {
				t.Errorf("snapshot product: %s", snap.ProductID)
			}
			snapshots++
			return nil
		},
		getProduct: func(context.Context, string) (store.Product, error) {
			filled := unmatched
			filled.PriceCharting = &store.PCMeta{PCProductID: 7042, MatchConfidence: 1.0}
			return filled, nil
		},
	}
	// games is zero-valued: the found path must make no IGDB call.
	h := newUnitHandlers(st, &stubGames{}, prices, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("fill resolve: %d %s", rec.Code, rec.Body.String())
	}
	var p api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Pricecharting == nil || p.Pricecharting.PcProductId != 7042 {
		t.Fatalf("response must serve the filled doc: %+v", p.Pricecharting)
	}
	if snapshots != 1 {
		t.Fatalf("a landed fill snapshots once, got %d", snapshots)
	}

	// Fill lost the race: no snapshot (appendSnapshot unset would
	// panic), current doc served.
	st2 := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) { return unmatched, nil },
		setPriceChartingIfMissing: func(context.Context, string, *store.PCMeta) (bool, error) {
			return false, nil
		},
		getProduct: func(context.Context, string) (store.Product, error) {
			winner := unmatched
			winner.PriceCharting = &store.PCMeta{PCProductID: 5011, MatchConfidence: 0.9}
			return winner, nil
		},
	}
	rec = serveUnit(t, newUnitHandlers(st2, &stubGames{}, prices, newStubCache()), env, http.MethodPost, "/products/resolve", tok, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("lost fill race: %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Pricecharting == nil || p.Pricecharting.PcProductId != 5011 {
		t.Fatalf("lost race must serve the winner's mapping: %+v", p.Pricecharting)
	}
}

func TestUnitResolve_GameManualMatchAnchoredIsUntouched(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	mapped := store.Product{ID: "22222222-2222-2222-2222-222222222222", Type: "game", Name: "Chrono Trigger",
		PriceCharting: &store.PCMeta{PCProductID: 5011, MatchConfidence: 0.93}}
	// prices and every store mutator are unset: ANY provider call or
	// write on this path panics - the mapped product returns as-is.
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) { return mapped, nil },
	}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19, "pc_product_id": 7042})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body.String())
	}
	var p api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Pricecharting == nil || p.Pricecharting.PcProductId != 5011 {
		t.Fatalf("existing mapping must be served untouched: %+v", p.Pricecharting)
	}
}

func TestUnitResolve_GameManualMatchLostCreateRaceFills(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 1011, Name: "Chrono Trigger", Platforms: []igdb.Named{{ID: 19, Name: "Super Nintendo Entertainment System"}}}}, nil
	}}
	loose := int64(12500)
	prices := &stubPrices{product: func(_ context.Context, id int64) (pricecharting.Product, error) {
		return pricecharting.Product{ID: id, Name: "Chrono Trigger [PAL]", ConsoleName: "PAL Super Nintendo", LoosePriceCents: &loose}, nil
	}}
	winnerID := "77777777-7777-7777-7777-777777777777"
	var filledID string
	var snapshots int
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		upsertRaw: func(context.Context, []igdb.Game, time.Time) error { return nil },
		createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
			// Lost race: the winner's doc comes back under another id,
			// and the winner (a plain resolve whose auto-match missed)
			// carries no mapping.
			return store.Product{ID: winnerID, Type: "game", Name: p.Name}, nil
		},
		setPriceChartingIfMissing: func(_ context.Context, id string, m *store.PCMeta) (bool, error) {
			filledID = id
			if m == nil || m.PCProductID != 7042 {
				t.Errorf("must reuse the already-fetched meta: %+v", m)
			}
			return true, nil
		},
		appendSnapshot: func(context.Context, store.Snapshot) error { snapshots++; return nil },
		getProduct: func(context.Context, string) (store.Product, error) {
			return store.Product{ID: winnerID, Type: "game", Name: "Chrono Trigger",
				PriceCharting: &store.PCMeta{PCProductID: 7042, MatchConfidence: 1.0}}, nil
		},
		platformsFetchedAt: func(context.Context) (time.Time, error) { return time.Now(), nil },
		listPlatforms: func(context.Context) ([]store.CatalogPlatform, error) {
			return []store.CatalogPlatform{{ID: 19, Name: "Super Nintendo Entertainment System"}}, nil
		},
	}
	h := newUnitHandlers(st, games, prices, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19, "pc_product_id": 7042})
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", rec.Code, rec.Body.String())
	}
	if filledID != winnerID {
		t.Fatalf("the winner's doc must be filled, got %q", filledID)
	}
	// Exactly one snapshot: the fill's. The create-path snapshot gate
	// (created.ID == p.ID) correctly does not fire on a lost race.
	if snapshots != 1 {
		t.Fatalf("want exactly the fill snapshot, got %d", snapshots)
	}
	var p api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Id.String() != winnerID || p.Pricecharting == nil || p.Pricecharting.PcProductId != 7042 {
		t.Fatalf("response must serve the filled winner: %s %+v", p.Id, p.Pricecharting)
	}
}

func TestUnitResolve_GameManualMatchFillUnknownListing404(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	unmatched := store.Product{ID: "11111111-1111-1111-1111-111111111111", Type: "game", Name: "Terranigma"}
	// setPriceChartingIfMissing is unset: a 404 must abort before any
	// store write.
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) { return unmatched, nil },
	}
	prices := &stubPrices{product: func(context.Context, int64) (pricecharting.Product, error) {
		return pricecharting.Product{}, pricecharting.ErrNotFound
	}}
	rec := serveUnit(t, newUnitHandlers(st, &stubGames{}, prices, newStubCache()), env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19, "pc_product_id": 999999})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown_pc_product") {
		t.Fatalf("fill with unknown listing: %d %s", rec.Code, rec.Body.String())
	}
}

func TestResolve_PCListingCreatesAndDedupes(t *testing.T) {
	s := newStack(t)
	body := map[string]any{"type": "pc_listing", "pc_product_id": 5001}
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first resolve: status %d", resp.StatusCode)
	}
	first := decodeBody[api.Product](t, resp)
	if string(first.Type) != "pc_listing" {
		t.Fatalf("type = %s, want pc_listing", first.Type)
	}
	if first.Igdb != nil {
		t.Fatal("pc_listing products must carry no igdb subdoc")
	}
	if first.Pricecharting == nil || first.Pricecharting.PcProductId != 5001 {
		t.Fatal("pc_listing product must carry the picked listing")
	}
	if first.Pricecharting.MatchConfidence != 1.0 || first.Pricecharting.Verified {
		t.Fatalf("want confidence 1.0 verified false, got %v/%v",
			first.Pricecharting.MatchConfidence, first.Pricecharting.Verified)
	}
	if first.Pricecharting.LooseCents == nil {
		t.Fatal("stub listings always price; want current prices on create")
	}
	if first.Region != nil || first.Edition != nil || first.Variant != nil {
		t.Fatal("pc_listing identity fields must stay empty")
	}

	// Same listing again, with stray variant fields: same product, ignored.
	body["variant"] = "players choice"
	resp = s.do(http.MethodPost, "/products/resolve", s.userToken(), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second resolve: status %d", resp.StatusCode)
	}
	second := decodeBody[api.Product](t, resp)
	if second.Id != first.Id {
		t.Fatalf("dedup failed: %s vs %s", second.Id, first.Id)
	}

	// The create appended the day-zero snapshot.
	hist := s.do(http.MethodPost, "/products/price-history:batch", s.userToken(),
		map[string]any{"product_ids": []string{first.Id.String()}})
	series := decodeBody[api.PriceHistoryResponse](t, hist).Series
	if len(series[first.Id.String()]) != 1 {
		t.Fatalf("want exactly the initial snapshot, got %d points", len(series[first.Id.String()]))
	}
}

func TestUnitResolve_PCListingValidationAndErrors(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{}
	prices := &stubPrices{}
	h := newUnitHandlers(st, &stubGames{}, prices, newStubCache())

	// Missing pc_product_id.
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "pc_listing"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing id: status %d", rec.Code)
	}

	// Unknown listing id -> 404 unknown_pc_product.
	prices.product = func(ctx context.Context, id int64) (pricecharting.Product, error) {
		return pricecharting.Product{}, pricecharting.ErrNotFound
	}
	st.findProduct = func(ctx context.Context, key store.ProductKey) (store.Product, error) {
		return store.Product{}, store.ErrNotFound
	}
	rec = serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "pc_listing", "pc_product_id": 999999})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: status %d", rec.Code)
	}

	// Provider down -> 502 upstream_unavailable.
	prices.product = func(ctx context.Context, id int64) (pricecharting.Product, error) {
		return pricecharting.Product{}, errors.New("boom")
	}
	rec = serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "pc_listing", "pc_product_id": 5001})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("provider down: status %d", rec.Code)
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

func TestUnitBatchPriceHistoryGroupsAndWindows(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	fixed := time.Date(2026, time.July, 5, 12, 0, 0, 0, time.UTC)
	var gotIDs []string
	var gotSince time.Time
	loose := int64(1200)
	st := &stubStore{
		snapshotsSince: func(_ context.Context, ids []string, since time.Time) (map[string][]store.Snapshot, error) {
			gotIDs, gotSince = ids, since
			return map[string][]store.Snapshot{
				ids[0]: {{ProductID: ids[0], CapturedAt: fixed.AddDate(0, 0, -3), LooseCents: &loose}},
			}, nil
		},
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	h.now = func() time.Time { return fixed }

	idA, idB := uuid.NewString(), uuid.NewString()
	rec := serveUnit(t, h, env, http.MethodPost, "/products/price-history:batch", tok,
		map[string]any{"product_ids": []string{idA, idB}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(gotIDs) != 2 {
		t.Fatalf("store saw %d ids, want 2", len(gotIDs))
	}
	if want := fixed.AddDate(0, 0, -90); !gotSince.Equal(want) {
		t.Fatalf("default window: since %v, want %v", gotSince, want)
	}
	var resp struct {
		Series map[string][]struct {
			CapturedAt time.Time `json:"captured_at"`
			LooseCents *int64    `json:"loose_cents"`
		} `json:"series"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Series) != 1 || len(resp.Series[idA]) != 1 || *resp.Series[idA][0].LooseCents != 1200 {
		t.Fatalf("series wrong: %s", rec.Body)
	}
}

func TestUnitBatchPriceHistoryRejectsBadInput(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{} // any store call would panic: rejection happens first
	h := newUnitHandlers(st, nil, nil, newStubCache())
	router := NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil })

	// serveUnit marshals its body param through json.Marshal, which
	// cannot produce a deliberately malformed payload or a pre-built
	// literal array of quoted ids; post issues the raw body exactly
	// like the bad-body case in TestUnitBatchPrices_CapAndBadBody above.
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/products/price-history:batch", bytes.NewReader([]byte(body)))
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	ids := make([]string, 501)
	for i := range ids {
		ids[i] = fmt.Sprintf("%q", uuid.NewString())
	}
	over := fmt.Sprintf(`{"product_ids":[%s]}`, strings.Join(ids, ","))

	cases := []struct {
		name, body string
	}{
		{"malformed", `{"product_ids":`},
		{"too many ids", over},
		{"days too small", fmt.Sprintf(`{"product_ids":[%q],"days":0}`, uuid.NewString())},
		{"days too large", fmt.Sprintf(`{"product_ids":[%q],"days":366}`, uuid.NewString())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "invalid_body") {
				t.Fatalf("problem code missing: %s", rec.Body)
			}
		})
	}
}

func TestUnitBatchPriceHistoryStoreFailureIs500(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{
		snapshotsSince: func(context.Context, []string, time.Time) (map[string][]store.Snapshot, error) {
			return nil, errors.New("mongo down")
		},
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/price-history:batch", tok,
		map[string]any{"product_ids": []string{uuid.NewString()}})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", rec.Code)
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

// TestUnitRecommendations_LibraryTooLargeRejected pins the contract's
// maxItems bound: one entry past it answers 400 before any store or
// provider call (the zero-field stubs would panic if reached).
func TestUnitRecommendations_LibraryTooLargeRejected(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	h := newUnitHandlers(&stubStore{}, &stubGames{}, nil, newStubCache())
	library := make([]map[string]any, 2501)
	for i := range library {
		library[i] = map[string]any{"igdb_game_id": i + 1}
	}
	rec := serveUnit(t, h, env, http.MethodPost, "/recommendations:score", tok,
		map[string]any{"library": library})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "library_too_large") {
		t.Fatalf("want library_too_large problem, got %s", rec.Body.String())
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

// TestRefresh_WalksPCListingProducts pins that the daily walk is not
// scoped to "game" products: ListPriced filters on the PriceCharting
// mapping existing at all, so a pc_listing price-anchor product (no
// igdb subdoc, created straight off a listing id) must be walked and
// snapshotted exactly like a resolved game.
func TestRefresh_WalksPCListingProducts(t *testing.T) {
	s := newStack(t)
	created := decodeBody[api.Product](t,
		s.do(http.MethodPost, "/products/resolve", s.userToken(),
			map[string]any{"type": "pc_listing", "pc_product_id": 5099}))

	resp := s.doInternal(testInternalToken) // no JWT: the CronJob path
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("internal refresh: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	ctx := context.Background()
	waitFor(t, 10*time.Second, func() bool {
		// The create-time snapshot plus one walk snapshot.
		n, err := s.mdb.Collection("price_snapshots").CountDocuments(ctx, map[string]any{})
		return err == nil && n == 2
	})

	hist := s.do(http.MethodPost, "/products/price-history:batch", s.userToken(),
		map[string]any{"product_ids": []string{created.Id.String()}})
	series := decodeBody[api.PriceHistoryResponse](t, hist).Series
	if len(series[created.Id.String()]) < 2 {
		t.Fatalf("walk must snapshot pc_listing products: got %d points", len(series[created.Id.String()]))
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
		nil, nil, nil, newStubCache(), Options{
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

func TestUnitRunRefresh_StopsEarlyOnContextCancellation(t *testing.T) {
	prods := make([]store.Product, 5)
	for i := range prods {
		prods[i] = store.Product{ID: fmt.Sprintf("p%d", i), PriceCharting: &store.PCMeta{PCProductID: int64(i)}}
	}
	st := &stubStore{
		listPriced:       func(context.Context) ([]store.Product, error) { return prods, nil },
		setCurrentPrices: func(context.Context, string, store.PriceQuote, time.Time) error { return nil },
		appendSnapshot:   func(context.Context, store.Snapshot) error { return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	prices := &stubPrices{product: func(context.Context, int64) (pricecharting.Product, error) {
		calls++
		if calls == 2 {
			// The budget expires partway through the walk (after the
			// 2nd of 5 products): the next iteration's ctx.Err() check
			// must stop the walk instead of visiting the rest.
			cancel()
		}
		return pricecharting.Product{ID: 1, Name: "P", ConsoleName: "C"}, nil
	}}
	h := newUnitHandlers(st, nil, prices, newStubCache())

	h.runRefresh(ctx)

	if calls != 2 {
		t.Fatalf("walk must stop between products once ctx is done: price provider called %d times, want 2", calls)
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

// ---------------------------------------------------------------
// FX rates
// ---------------------------------------------------------------

func TestUnitGetFxLatest_ServesSnapshot(t *testing.T) {
	rates := &stubFX{latest: func(context.Context) (fx.Rates, error) {
		return fx.Rates{Base: "USD", Date: "2026-07-01", Rates: map[string]float64{"EUR": 0.5, "JPY": 150}}, nil
	}}
	// Build the server through this file's usual harness, substituting
	// the fx stub; then issue an authed GET /fx/latest the same way the
	// neighboring GET tests do.
	rec := doAuthedFxRequest(t, rates)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Base  string             `json:"base"`
		Date  string             `json:"date"`
		Rates map[string]float64 `json:"rates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Base != "USD" || got.Date != "2026-07-01" || got.Rates["EUR"] != 0.5 || got.Rates["JPY"] != 150 {
		t.Fatalf("snapshot: %+v", got)
	}
}

func TestUnitGetFxLatest_ColdFailureAnswers502(t *testing.T) {
	rates := &stubFX{latest: func(context.Context) (fx.Rates, error) {
		return fx.Rates{}, errors.New("upstream down")
	}}
	rec := doAuthedFxRequest(t, rates)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status: %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_unavailable") {
		t.Fatalf("problem code missing: %s", rec.Body.String())
	}
}
