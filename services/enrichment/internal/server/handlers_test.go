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
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/cache"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/db"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/fx"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/match"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
	"github.com/levonn-dev/vgkeep/services/enrichment/migrations"
)

// ---------------------------------------------------------------
// Stub doubles (function fields; a nil field panics loudly).
// ---------------------------------------------------------------

// stubStore implements Store via function fields.
type stubStore struct {
	findProduct                  func(ctx context.Context, key store.ProductKey) (store.Product, error)
	createProduct                func(ctx context.Context, p store.Product) (store.Product, error)
	getProduct                   func(ctx context.Context, id string) (store.Product, error)
	setIGDB                      func(ctx context.Context, id string, m store.IGDBMeta) error
	setPriceCharting             func(ctx context.Context, id string, m *store.PCMeta) error
	promoteProduct               func(ctx context.Context, id string, igdbMeta *store.IGDBMeta, platform *store.Platform, pc *store.PCMeta) error
	setCurrentPrices             func(ctx context.Context, id string, q store.PriceQuote, asOf time.Time) error
	listPriced                   func(ctx context.Context) ([]store.Product, error)
	listUnmatchedProducts        func(ctx context.Context, limit, offset int) ([]store.Product, int64, error)
	deleteUnmatchedProduct       func(ctx context.Context, id string) (bool, error)
	listIGDBProducts             func(ctx context.Context) ([]store.Product, error)
	productsByIDs                func(ctx context.Context, ids []string) ([]store.Product, error)
	searchByName                 func(ctx context.Context, q string, limit int) ([]store.Product, error)
	searchCommunityProducts      func(ctx context.Context, types []string, q string, limit int) ([]store.Product, error)
	listCommunityRegionDocs      func(ctx context.Context) ([]store.CommunityRegionRef, error)
	setCommunityRegion           func(ctx context.Context, id, region string) error
	listCommunityProducts        func(ctx context.Context) ([]store.Product, error)
	listCommunityProductsPage    func(ctx context.Context, limit, offset int) ([]store.Product, int64, error)
	replacePromoteCandidates     func(ctx context.Context, id string, cands []store.PromoteCandidate) error
	listPromoteCandidateProducts func(ctx context.Context, limit, offset int, productID string) ([]store.Product, int64, error)
	dismissPromoteCandidate      func(ctx context.Context, id, provider string, providerID int64) error
	upsertRaw                    func(ctx context.Context, games []igdb.Game, fetchedAt time.Time) error
	rawByIDs                     func(ctx context.Context, ids []int64) ([]store.RawGame, error)
	upsertPlatforms              func(ctx context.Context, ps []igdb.Platform, fetchedAt time.Time) error
	listPlatforms                func(ctx context.Context) ([]store.CatalogPlatform, error)
	platformsFetchedAt           func(ctx context.Context) (time.Time, error)
	appendSnapshot               func(ctx context.Context, s store.Snapshot) error
	snapshotsSince               func(ctx context.Context, ids []string, since time.Time) (map[string][]store.Snapshot, error)
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

func (s *stubStore) PromoteProduct(ctx context.Context, id string, igdbMeta *store.IGDBMeta, platform *store.Platform, pc *store.PCMeta) error {
	if s.promoteProduct == nil {
		panic("unexpected PromoteProduct")
	}
	return s.promoteProduct(ctx, id, igdbMeta, platform, pc)
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

func (s *stubStore) ListUnmatchedProducts(ctx context.Context, limit, offset int) ([]store.Product, int64, error) {
	if s.listUnmatchedProducts == nil {
		panic("unexpected ListUnmatchedProducts")
	}
	return s.listUnmatchedProducts(ctx, limit, offset)
}

func (s *stubStore) DeleteUnmatchedProduct(ctx context.Context, id string) (bool, error) {
	if s.deleteUnmatchedProduct == nil {
		panic("unexpected DeleteUnmatchedProduct")
	}
	return s.deleteUnmatchedProduct(ctx, id)
}

func (s *stubStore) ListIGDBProducts(ctx context.Context) ([]store.Product, error) {
	if s.listIGDBProducts == nil {
		panic("unexpected ListIGDBProducts")
	}
	return s.listIGDBProducts(ctx)
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

func (s *stubStore) SearchCommunityProducts(ctx context.Context, types []string, q string, limit int) ([]store.Product, error) {
	if s.searchCommunityProducts == nil {
		panic("unexpected SearchCommunityProducts")
	}
	return s.searchCommunityProducts(ctx, types, q, limit)
}

func (s *stubStore) ListCommunityRegionDocs(ctx context.Context) ([]store.CommunityRegionRef, error) {
	if s.listCommunityRegionDocs == nil {
		panic("unexpected ListCommunityRegionDocs")
	}
	return s.listCommunityRegionDocs(ctx)
}

func (s *stubStore) SetCommunityRegion(ctx context.Context, id, region string) error {
	if s.setCommunityRegion == nil {
		panic("unexpected SetCommunityRegion")
	}
	return s.setCommunityRegion(ctx, id, region)
}

func (s *stubStore) ListCommunityProducts(ctx context.Context) ([]store.Product, error) {
	if s.listCommunityProducts == nil {
		panic("unexpected ListCommunityProducts")
	}
	return s.listCommunityProducts(ctx)
}

func (s *stubStore) ListCommunityProductsPage(ctx context.Context, limit, offset int) ([]store.Product, int64, error) {
	if s.listCommunityProductsPage == nil {
		panic("unexpected ListCommunityProductsPage")
	}
	return s.listCommunityProductsPage(ctx, limit, offset)
}

func (s *stubStore) ReplacePromoteCandidates(ctx context.Context, id string, cands []store.PromoteCandidate) error {
	if s.replacePromoteCandidates == nil {
		panic("unexpected ReplacePromoteCandidates")
	}
	return s.replacePromoteCandidates(ctx, id, cands)
}

func (s *stubStore) ListPromoteCandidateProducts(ctx context.Context, limit, offset int, productID string) ([]store.Product, int64, error) {
	if s.listPromoteCandidateProducts == nil {
		panic("unexpected ListPromoteCandidateProducts")
	}
	return s.listPromoteCandidateProducts(ctx, limit, offset, productID)
}

func (s *stubStore) DismissPromoteCandidate(ctx context.Context, id, provider string, providerID int64) error {
	if s.dismissPromoteCandidate == nil {
		panic("unexpected DismissPromoteCandidate")
	}
	return s.dismissPromoteCandidate(ctx, id, provider, providerID)
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
	searchGames         func(ctx context.Context, q string, limit int) ([]igdb.Game, error)
	gamesByIDs          func(ctx context.Context, ids []int64) ([]igdb.Game, error)
	popularGames        func(ctx context.Context, genreIDs []int64, excludeIDs []int64, limit int) ([]igdb.Game, error)
	platforms           func(ctx context.Context) ([]igdb.Platform, error)
	searchLocalizations func(ctx context.Context, q string, limit int) ([]int64, error)
}

var _ GameProvider = (*stubGames)(nil)

func (s *stubGames) SearchGames(ctx context.Context, q string, limit int) ([]igdb.Game, error) {
	if s.searchGames == nil {
		panic("unexpected SearchGames")
	}
	return s.searchGames(ctx, q, limit)
}

func (s *stubGames) SearchLocalizations(ctx context.Context, q string, limit int) ([]int64, error) {
	if s.searchLocalizations == nil {
		panic("unexpected SearchLocalizations")
	}
	return s.searchLocalizations(ctx, q, limit)
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
	err       error
	search    map[string][]byte
	prods     map[string][]byte
	platforms []byte
	puts      int
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

func (c *stubCache) GetPlatforms(_ context.Context) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	return c.platforms, nil
}

func (c *stubCache) PutPlatforms(_ context.Context, body []byte, _ time.Duration) error {
	if c.err != nil {
		return c.err
	}
	c.platforms = body
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
		SearchCacheTTL:   time.Hour,
		ProductCacheTTL:  time.Minute,
		IGDBRefreshAfter: 720 * time.Hour,
		Logger:           slog.New(slog.DiscardHandler),
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
		SearchCacheTTL:   time.Hour,
		ProductCacheTTL:  time.Minute,
		IGDBRefreshAfter: 720 * time.Hour,
		Logger:           slog.New(slog.DiscardHandler),
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

// One MongoDB and one valkey container serve this whole package (the
// same shared-container pattern as the store package's fixture): the
// per-test containers this replaces spent most of the package's
// runtime on boots. Each test still opens on a fresh, fully migrated
// database and an empty keyspace via the drop + re-migrate and
// FlushAll resets in newStack. No Terminate: the testcontainers
// reaper collects both containers when the test process exits.
var (
	sharedMongo struct {
		once sync.Once
		url  string
		err  error
	}
	sharedVK struct {
		once sync.Once
		url  string
		err  error
	}
)

func newStack(t *testing.T) *stack {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()

	sharedMongo.once.Do(func() {
		mc, err := tcmongo.Run(ctx, "mongo:8")
		if err != nil {
			sharedMongo.err = err
			return
		}
		sharedMongo.url, sharedMongo.err = mc.ConnectionString(ctx)
	})
	if sharedMongo.err != nil {
		t.Fatal(sharedMongo.err)
	}
	mclient, err := db.Connect(ctx, sharedMongo.url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mclient.Disconnect(context.Background()) })
	if err := mclient.Database("enrichment").Drop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, sharedMongo.url, "enrichment", migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mdb := mclient.Database("enrichment")
	st := store.New(mdb)

	sharedVK.once.Do(func() {
		vk, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine")
		if err != nil {
			sharedVK.err = err
			return
		}
		sharedVK.url, sharedVK.err = vk.ConnectionString(ctx)
	})
	if sharedVK.err != nil {
		t.Fatal(sharedVK.err)
	}
	rdb, err := valkeykit.Connect(ctx, sharedVK.url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	if err := rdb.FlushAll(ctx).Err(); err != nil {
		t.Fatal(err)
	}

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
		SearchCacheTTL:   time.Hour,
		ProductCacheTTL:  time.Minute,
		IGDBRefreshAfter: 720 * time.Hour,
		Logger:           slog.New(slog.DiscardHandler),
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

func (s *stack) serviceToken() string {
	return s.env.serviceToken(s.t, "svc:catalog-refresh")
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

// TestSearch_GameResultCarriesReleaseRegions pins gameResult's platform
// refs: OoT (fixture 1001) has three dated release_dates rows on
// platform 4 (Nintendo 64) - japan, north_america, then europe by
// date - so the platform ref's release_regions must carry that exact
// order. Hardware results never carry a platform ref at all, so
// release_regions can never leak onto them.
func TestSearch_GameResultCarriesReleaseRegions(t *testing.T) {
	s := newStack(t)

	resp := s.do(http.MethodGet, "/search?type=game&q=zelda", s.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: %d", resp.StatusCode)
	}
	res := decodeBody[api.SearchResults](t, resp)
	if len(res.Results) == 0 {
		t.Fatal("want at least the OoT hit")
	}
	first := res.Results[0] // pinned to OoT by TestSearch_GameThroughStubAndCache
	if first.Platforms == nil || len(*first.Platforms) == 0 {
		t.Fatalf("want at least one platform ref, got %+v", first.Platforms)
	}
	n64 := (*first.Platforms)[0]
	if n64.IgdbPlatformId != 4 {
		t.Fatalf("want the Nintendo 64 platform ref, got %+v", n64)
	}
	if n64.ReleaseRegions == nil {
		t.Fatal("want release_regions populated on a game platform ref")
	}
	want := []string{"japan", "north_america", "europe"}
	if !slices.Equal(*n64.ReleaseRegions, want) {
		t.Fatalf("release_regions order: got %v, want %v", *n64.ReleaseRegions, want)
	}

	hw := s.do(http.MethodGet, "/search?type=hardware&q=nintendo+64", s.userToken(), nil)
	hwRes := decodeBody[api.SearchResults](t, hw)
	if len(hwRes.Results) == 0 {
		t.Fatal("want at least one hardware hit")
	}
	for _, r := range hwRes.Results {
		if r.Platforms != nil {
			t.Fatalf("hardware results must carry no platform ref at all: %+v", r)
		}
	}
}

// TestSearch_GameResultCarriesBundlesWithoutAnnotation asserts a
// canonical-name search returns the game's localization bundles but no
// matched_region: the query recognized the canonical name, so nothing
// needs flagging or preselecting. The stub provider matches Name only,
// so this exercises the canonical-match path; matchedRegion's own
// containment/translit logic is unit-tested directly.
func TestSearch_GameResultCarriesBundlesWithoutAnnotation(t *testing.T) {
	s := newStack(t)

	resp := s.do(http.MethodGet, "/search?type=game&q=Secret+of+Mana", s.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: %d", resp.StatusCode)
	}
	res := decodeBody[api.SearchResults](t, resp)
	if len(res.Results) != 1 {
		t.Fatalf("want the single Secret of Mana hit, got %d: %+v", len(res.Results), res.Results)
	}
	r := res.Results[0]
	if r.MatchedRegion != nil {
		t.Fatalf("canonical-name match must not annotate a region: %+v", *r.MatchedRegion)
	}
	if r.Localizations == nil || len(*r.Localizations) != 2 {
		t.Fatalf("want the JP + EU bundles, got %+v", r.Localizations)
	}
	locs := *r.Localizations
	// Sorted by region: EU before ja-JP.
	if locs[0].Region != "EU" || locs[0].CoverUrl == nil {
		t.Fatalf("EU bundle: %+v", locs[0])
	}
	if locs[1].Region != "ja-JP" || locs[1].Name == nil || *locs[1].Name != "聖剣伝説2" ||
		locs[1].Translit == nil || *locs[1].Translit != "Seiken Densetsu 2" {
		t.Fatalf("ja-JP bundle: %+v", locs[1])
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
	st := &stubStore{
		searchByName: func(_ context.Context, q string, _ int) ([]store.Product, error) {
			return []store.Product{{
				ID: "11111111-1111-1111-1111-111111111111", Type: "game", Name: "Chrono Trigger",
				Platform: &store.Platform{IGDBID: 19, Name: "Super Nintendo Entertainment System"},
				IGDB:     &store.IGDBMeta{GameID: 1011, FetchedAt: time.Now(), FirstReleaseDate: releaseDate},
			}}, nil
		},
		// Empty lane: this test pins the degraded-fallback result shape.
		searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil },
	}
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
	st := &stubStore{
		searchByName:            func(context.Context, string, int) ([]store.Product, error) { return nil, nil },
		searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil },
	}
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
	// Empty lane: this test is about the cache failure, not the lane.
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, c)
	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cache outage must not fail search: %d", rec.Code)
	}
}

// The provider's relevance order is loose about exactness: an exact
// name must lead, the most-rated exact first, everything else in
// provider order.
func TestUnitSearch_ExactNameRanksFirst(t *testing.T) {
	env := newAuthEnv(t)
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{
			{ID: 1, Name: "Super Mario 64 DS"},
			{ID: 2, Name: "Super Mario Odyssey"},
			{ID: 3, Name: "Super Mario 64 (Shindou Pak Taiou Version)", TotalRatingCount: 12},
			{ID: 4, Name: "Super Mario 64", TotalRatingCount: 908},
		}, nil
	}}
	// Empty lane: this test is about provider rank order, not the lane.
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=Super+Mario+64", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	var res api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	got := make([]int64, 0, len(res.Results))
	for _, r := range res.Results {
		got = append(got, *r.IgdbGameId)
	}
	// 4 and 3 both normalize to the query (brackets strip); the rating
	// count puts the widely known release first. 1 and 2 keep provider
	// order.
	if want := []int64{4, 3, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("rank order: got %v, want %v", got, want)
	}
}

// The provider's tokenizer misses possessive-less listing names when
// the query carries the possessive; the outgoing query drops it
// (evidence: /api/products probes, 2026-07-15).
func TestUnitSearch_PCQueryDropsPossessive(t *testing.T) {
	env := newAuthEnv(t)
	var gotQ string
	prices := &stubPrices{search: func(_ context.Context, q string) ([]pricecharting.Product, error) {
		gotQ = q
		return nil, nil
	}}
	h := newUnitHandlers(nil, nil, prices, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=pc_listing&q=Michael+Jackson's+Moonwalker", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	if gotQ != "Michael Jackson Moonwalker" {
		t.Fatalf("provider query must drop the possessive, got %q", gotQ)
	}
}

func TestUnitSearch_PossessiveNamesRankExact(t *testing.T) {
	env := newAuthEnv(t)
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{
			{ID: 1, Name: "Moonwalker"},
			{ID: 2, Name: "Michael Jackson's Moonwalker"},
		}, nil
	}}
	// Empty lane: this test is about provider rank order, not the lane.
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=Michael+Jackson+Moonwalker", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	var res api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 || *res.Results[0].IgdbGameId != 2 {
		t.Fatalf("possessive name must rank exact for the bare query: %+v", res.Results)
	}
}

func TestMatchedRegion(t *testing.T) {
	g := igdb.Game{Name: "Trials of Mana",
		AlternativeNames:  []igdb.AlternativeName{{Name: "Seiken Densetsu 3", Comment: "Japanese title - romanization"}},
		GameLocalizations: []igdb.GameLocalization{{Name: "聖剣伝説 3", Region: igdb.LocalizationRegion{Identifier: "ja-JP"}}}}
	cases := []struct{ q, want string }{
		{"Trials of Mana", ""},         // canonical wins, no annotation
		{"trials of mana", ""},         // normalized canonical
		{"Seiken Densetsu 3", "ja-JP"}, // translit exact
		{"seiken densetsu", "ja-JP"},   // containment
		{"聖剣伝説 3", "ja-JP"},            // native exact
		{"聖剣", "ja-JP"},                // native containment, 2-rune non-latin guard
		{"se", ""},                     // below 3-rune latin guard
		{"a", ""},
		{"final fantasy", ""}, // no relation
	}
	for _, tc := range cases {
		if got := matchedRegion(tc.q, g); got != tc.want {
			t.Fatalf("matchedRegion(%q) = %q want %q", tc.q, got, tc.want)
		}
	}
}

// TestGameResult_MapsLocalizationBundles pins gameResult's localization
// mapping: the same non-empty-only pointer idiom toAPIProduct uses,
// never a pointer to an empty string.
func TestGameResult_MapsLocalizationBundles(t *testing.T) {
	g := igdb.Game{ID: 1016, Name: "Secret of Mana",
		AlternativeNames: []igdb.AlternativeName{{Name: "Seiken Densetsu 2", Comment: "Japanese title - romanization"}},
		GameLocalizations: []igdb.GameLocalization{
			{Name: "聖剣伝説2", Region: igdb.LocalizationRegion{Identifier: "ja-JP"}},
			{Region: igdb.LocalizationRegion{Identifier: "EU"}, Cover: &igdb.Cover{ImageID: "stub-eu-cover"}},
		},
	}
	res := gameResult(g)
	if res.Localizations == nil || len(*res.Localizations) != 2 {
		t.Fatalf("want the JP + EU bundles, got %+v", res.Localizations)
	}
	// Sorted by region: EU before ja-JP.
	eu, jp := (*res.Localizations)[0], (*res.Localizations)[1]
	if eu.Region != "EU" || eu.Name != nil || eu.Translit != nil {
		t.Fatalf("EU bundle must carry no name/translit pointer: %+v", eu)
	}
	wantEUCover := "https://images.igdb.com/igdb/image/upload/t_cover_big/stub-eu-cover.jpg"
	if eu.CoverUrl == nil || *eu.CoverUrl != wantEUCover {
		t.Fatalf("EU bundle cover_url: %+v", eu.CoverUrl)
	}
	if jp.Region != "ja-JP" || jp.CoverUrl != nil {
		t.Fatalf("ja-JP bundle must carry no cover pointer: %+v", jp)
	}
	if jp.Name == nil || *jp.Name != "聖剣伝説2" {
		t.Fatalf("ja-JP bundle name: %+v", jp.Name)
	}
	if jp.Translit == nil || *jp.Translit != "Seiken Densetsu 2" {
		t.Fatalf("ja-JP bundle translit: %+v", jp.Translit)
	}
}

// TestPlatformReleaseRegions pins platformReleaseRegions's contract
// (see its doc for the ordering and twin-platform rules) against the
// Mr. Gimmick NES/Famicom shape, Puyo Puyo SUN's repeated-region
// shape, dedup, and the platform-0 / unknown-region edge cases.
func TestPlatformReleaseRegions(t *testing.T) {
	day := func(y int, m time.Month, d int) int64 {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
	}
	cases := []struct {
		name       string
		game       igdb.Game
		platformID int64
		want       []string
	}{
		{
			name: "Mr Gimmick shape: NES row, not folded with its Famicom twin",
			game: igdb.Game{ReleaseDates: []igdb.ReleaseDate{
				{Date: day(1992, time.July, 3), Platform: 18, Region: 1},  // NES europe
				{Date: day(1992, time.April, 3), Platform: 99, Region: 5}, // Famicom japan
			}},
			platformID: 18,
			want:       []string{"europe"},
		},
		{
			name: "Mr Gimmick shape: the Famicom row, not folded onto its NES twin",
			game: igdb.Game{ReleaseDates: []igdb.ReleaseDate{
				{Date: day(1992, time.July, 3), Platform: 18, Region: 1},
				{Date: day(1992, time.April, 3), Platform: 99, Region: 5},
			}},
			platformID: 99,
			want:       []string{"japan"},
		},
		{
			name: "Puyo Puyo SUN shape: japan-only release repeated across platforms",
			game: igdb.Game{ReleaseDates: []igdb.ReleaseDate{
				{Date: day(1993, time.June, 25), Platform: 200, Region: 5},
				{Date: day(1993, time.June, 25), Platform: 201, Region: 5},
				{Date: day(1993, time.June, 25), Platform: 202, Region: 5},
			}},
			platformID: 201,
			want:       []string{"japan"},
		},
		{
			name: "worldwide served verbatim",
			game: igdb.Game{ReleaseDates: []igdb.ReleaseDate{
				{Date: day(2010, time.May, 1), Platform: 400, Region: 8},
			}},
			platformID: 400,
			want:       []string{"worldwide"},
		},
		{
			name: "ordering: earliest date first, dateless last, ties alphabetical",
			game: igdb.Game{ReleaseDates: []igdb.ReleaseDate{
				{Date: day(2000, time.January, 10), Platform: 500, Region: 2}, // north_america
				{Date: day(2000, time.January, 10), Platform: 500, Region: 1}, // europe, same date
				{Date: day(2000, time.March, 1), Platform: 500, Region: 5},    // japan, later
				{Platform: 500, Region: 9},                                    // korea, dateless
				{Platform: 500, Region: 6},                                    // china, dateless
			}},
			platformID: 500,
			want:       []string{"europe", "north_america", "japan", "china", "korea"},
		},
		{
			name: "dedupe: two rows same platform+region collapse to one, earliest governs order",
			game: igdb.Game{ReleaseDates: []igdb.ReleaseDate{
				{Date: day(2005, time.December, 1), Platform: 600, Region: 5}, // japan, seen first, later date
				{Date: day(2005, time.January, 1), Platform: 600, Region: 5},  // japan dup, earlier date wins
				{Date: day(2005, time.June, 1), Platform: 600, Region: 1},     // europe, in between
			}},
			platformID: 600,
			want:       []string{"japan", "europe"},
		},
		{
			name: "unknown region enum dropped",
			game: igdb.Game{ReleaseDates: []igdb.ReleaseDate{
				{Date: day(2010, time.May, 1), Platform: 700, Region: 999},
				{Date: day(2010, time.May, 1), Platform: 700, Region: 1},
			}},
			platformID: 700,
			want:       []string{"europe"},
		},
		{
			name: "platform-0 row dropped, even when queried as platform 0",
			game: igdb.Game{ReleaseDates: []igdb.ReleaseDate{
				{Date: day(2010, time.May, 1), Platform: 0, Region: 5},
			}},
			platformID: 0,
			want:       nil,
		},
		{
			name:       "no rows",
			game:       igdb.Game{},
			platformID: 4,
			want:       nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := platformReleaseRegions(tc.game, tc.platformID)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("platformReleaseRegions() = %v, want nil", got)
				}
				return
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("platformReleaseRegions() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestUnitSearch_LocalizationExactNameRanksFirst extends the
// exact-match float-to-top treatment to a region bundle's name or
// transliteration: a game whose own name is not exact but whose
// localized title exactly matches the query ranks with the other
// exacts, ahead of plain provider-order matches.
func TestUnitSearch_LocalizationExactNameRanksFirst(t *testing.T) {
	env := newAuthEnv(t)
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{
			{ID: 1, Name: "Trials of Mana Collection", TotalRatingCount: 500},
			{ID: 2, Name: "Trials of Mana",
				AlternativeNames:  []igdb.AlternativeName{{Name: "Seiken Densetsu 3", Comment: "Japanese title - romanization"}},
				GameLocalizations: []igdb.GameLocalization{{Name: "聖剣伝説 3", Region: igdb.LocalizationRegion{Identifier: "ja-JP"}}},
			},
		}, nil
	}}
	// Empty lane: this test is about provider rank order, not the lane.
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=Seiken+Densetsu+3", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	var res api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	got := make([]int64, 0, len(res.Results))
	for _, r := range res.Results {
		got = append(got, *r.IgdbGameId)
	}
	if want := []int64{2, 1}; !slices.Equal(got, want) {
		t.Fatalf("rank order: got %v, want %v", got, want)
	}
}

// TestSearch_NonLatinQueryReachesViaLocalizationLeg proves the
// supplementary leg end to end over the real stub: "ゼルダの伝説" is not
// a substring of fixture 1001's canonical Name ("The Legend of Zelda:
// Ocarina of Time"), so the primary provider search alone finds
// nothing - only the leg's match against the ja-JP game_localizations
// row surfaces the game.
func TestSearch_NonLatinQueryReachesViaLocalizationLeg(t *testing.T) {
	s := newStack(t)
	resp := s.do(http.MethodGet, "/search?type=game&q="+url.QueryEscape("ゼルダの伝説"), s.userToken(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search: %d", resp.StatusCode)
	}
	res := decodeBody[api.SearchResults](t, resp)
	if res.Degraded {
		t.Fatal("leg-served results must not be degraded")
	}
	if len(res.Results) != 1 || res.Results[0].IgdbGameId == nil || *res.Results[0].IgdbGameId != 1001 {
		t.Fatalf("want game 1001 via the localization leg, got %+v", res.Results)
	}
}

// TestUnitSearch_LatinQueryNeverCallsLocalizationLeg proves the
// non-latin trigger gate (hasNonLatinLetter): an all-latin query must
// cost zero SearchLocalizations calls.
func TestUnitSearch_LatinQueryNeverCallsLocalizationLeg(t *testing.T) {
	env := newAuthEnv(t)
	var legCalls int
	games := &stubGames{
		searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
			return []igdb.Game{{ID: 1011, Name: "Chrono Trigger"}}, nil
		},
		searchLocalizations: func(context.Context, string, int) ([]int64, error) {
			legCalls++
			return nil, nil
		},
	}
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono", env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	if legCalls != 0 {
		t.Fatalf("a latin query must never call the localization leg, got %d calls", legCalls)
	}
}

// TestUnitSearch_LocalizationLegErrorServesPrimaryResults proves the
// leg's failure mode: a SearchLocalizations error must never degrade
// or fail the request - the primary provider's results still serve,
// unflagged. legCalls pins that the leg actually ran (not merely
// absent) so this test cannot pass by accident.
func TestUnitSearch_LocalizationLegErrorServesPrimaryResults(t *testing.T) {
	env := newAuthEnv(t)
	var legCalls int
	games := &stubGames{
		searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
			return []igdb.Game{{ID: 1001, Name: "The Legend of Zelda: Ocarina of Time"}}, nil
		},
		searchLocalizations: func(context.Context, string, int) ([]int64, error) {
			legCalls++
			return nil, errors.New("igdb down")
		},
	}
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, games, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q="+url.QueryEscape("ゼルダの伝説"), env.token(t, "u1", []string{"user"}), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	if legCalls != 1 {
		t.Fatalf("want the leg to have run once, got %d calls", legCalls)
	}
	var res api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Degraded {
		t.Fatal("a leg failure must never degrade the answer")
	}
	if len(res.Results) != 1 || res.Results[0].IgdbGameId == nil || *res.Results[0].IgdbGameId != 1001 {
		t.Fatalf("primary results must still serve: %+v", res.Results)
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

// TestUnitGetProduct_StaleRefetchNilPlatformRebuildsAtPlatformZero pins
// refreshIGDBIfStale's defensive nil-Platform branch: a stale
// igdb-bearing product somehow missing its platform ref (games always
// carry one today; this documents the fallback rather than a reachable
// state) still refetches and rebuilds instead of panicking on a nil
// dereference, scoping the platform-id-0 release table to nothing and
// falling back to the game-level scalar.
func TestUnitGetProduct_StaleRefetchNilPlatformRebuildsAtPlatformZero(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	old := time.Now().Add(-1000 * time.Hour)
	scalar := time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)
	prod := store.Product{
		ID: "99999999-9999-9999-9999-999999999999", Type: "game", Name: "Chrono Trigger",
		// Platform deliberately nil.
		IGDB: &store.IGDBMeta{GameID: 1011, Name: "Chrono Trigger (stale)", FetchedAt: old},
	}

	var setCalled bool
	var setMeta store.IGDBMeta
	st := &stubStore{
		getProduct: func(context.Context, string) (store.Product, error) { return prod, nil },
		upsertRaw:  func(context.Context, []igdb.Game, time.Time) error { return nil },
		setIGDB: func(_ context.Context, id string, m store.IGDBMeta) error {
			setCalled = true
			setMeta = m
			return nil
		},
	}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return []igdb.Game{{
			ID: 1011, Name: "Chrono Trigger", FirstReleaseDate: scalar.Unix(),
			// A dated row for a real platform (19); platform id 0 (the
			// nil-Platform fallback) matches none of it.
			ReleaseDates: []igdb.ReleaseDate{{Date: scalar.Unix(), Platform: 19, Region: 5}},
		}}, nil
	}}
	h := newUnitHandlers(st, games, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/products/"+prod.ID, tok, nil)
	if rec.Code != http.StatusOK || !setCalled {
		t.Fatalf("nil-platform stale refetch: %d setCalled=%v %s", rec.Code, setCalled, rec.Body.String())
	}
	if setMeta.Name != "Chrono Trigger" {
		t.Fatalf("refetched projection must land: %+v", setMeta)
	}
	if setMeta.ReleaseDates == nil || len(setMeta.ReleaseDates) != 0 {
		t.Fatalf("platform id 0 must scope to an empty, non-nil release table: %#v", setMeta.ReleaseDates)
	}
	if !setMeta.FirstReleaseDate.Equal(scalar) {
		t.Fatalf("game-level scalar must survive with no platform rows: %v", setMeta.FirstReleaseDate)
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

// TestUnitGetProduct_ReleaseDatesMapped pins toAPIProduct's release_dates
// projection: a populated per-region table serves as rows on the wire,
// and an empty table (fetched, but IGDB listed no dated rows for the
// platform) serves the field absent rather than an empty array.
func TestUnitGetProduct_ReleaseDatesMapped(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	fresh := time.Now().Add(-1 * time.Hour) // well inside the 720h window
	day := time.Date(2003, time.September, 12, 0, 0, 0, 0, time.UTC)

	dated := store.Product{
		ID: "77777777-7777-7777-7777-777777777777", Type: "game", Name: "Chrono Trigger",
		IGDB: &store.IGDBMeta{GameID: 1011, Name: "Chrono Trigger", FetchedAt: fresh,
			ReleaseDates: []store.MetaReleaseDate{{Region: "japan", Date: day}}},
	}
	st := &stubStore{getProduct: func(context.Context, string) (store.Product, error) { return dated, nil }}
	// gamesByIDs is left nil: FetchedAt is fresh, so a correct mapping
	// test never touches the provider.
	h := newUnitHandlers(st, &stubGames{}, nil, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/products/"+dated.ID, tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get product: %d %s", rec.Code, rec.Body.String())
	}
	var p api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Igdb == nil || p.Igdb.ReleaseDates == nil || len(*p.Igdb.ReleaseDates) != 1 {
		t.Fatalf("release_dates not mapped: %+v", p.Igdb)
	}
	rd := (*p.Igdb.ReleaseDates)[0]
	if rd.Region != "japan" || !rd.Date.Equal(day) {
		t.Fatalf("release_dates row: %+v", rd)
	}

	// An empty table (fetched, IGDB lists no dated rows for the
	// platform) must serve the field absent, not an empty array.
	none := dated
	none.ID = "88888888-8888-8888-8888-888888888888"
	none.IGDB = &store.IGDBMeta{GameID: 1011, Name: "Chrono Trigger", FetchedAt: fresh, ReleaseDates: []store.MetaReleaseDate{}}
	st.getProduct = func(context.Context, string) (store.Product, error) { return none, nil }
	rec = serveUnit(t, h, env, http.MethodGet, "/products/"+none.ID, tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get product: %d %s", rec.Code, rec.Body.String())
	}
	var p2 api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &p2); err != nil {
		t.Fatal(err)
	}
	if p2.Igdb == nil {
		t.Fatal("igdb projection missing entirely")
	}
	if p2.Igdb.ReleaseDates != nil {
		t.Fatalf("empty release_dates must serve absent, got %+v", *p2.Igdb.ReleaseDates)
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

// TestResolve_GameLocalizationsMapped pins toAPIProduct's localizations
// projection for a real fixture: game 1001 (Ocarina of Time) carries one
// IGDB game_localizations row (ja-JP) that BundleLocalizations merges
// with the alternative_names romanization tag, and toAPIProduct must
// serve that bundle on the wire unchanged.
func TestResolve_GameLocalizationsMapped(t *testing.T) {
	s := newStack(t)

	// Ocarina of Time (fixture 1001) on Nintendo 64 (4).
	p := s.resolveGame(1001, 4)
	if p.Igdb == nil || p.Igdb.Localizations == nil || len(*p.Igdb.Localizations) != 1 {
		t.Fatalf("localizations not mapped: %+v", p.Igdb)
	}
	loc := (*p.Igdb.Localizations)[0]
	if loc.Region != "ja-JP" {
		t.Fatalf("localization region: %+v", loc)
	}
	if loc.Name == nil || *loc.Name != "ゼルダの伝説 時のオカリナ" {
		t.Fatalf("localization name: %+v", loc)
	}
	if loc.Translit == nil || *loc.Translit != "Zelda no Densetsu: Toki no Ocarina" {
		t.Fatalf("localization translit: %+v", loc)
	}
	wantCover := "https://images.igdb.com/igdb/image/upload/t_cover_big/stub-oot-jp.jpg"
	if loc.CoverUrl == nil || *loc.CoverUrl != wantCover {
		t.Fatalf("localization cover_url: %+v", loc)
	}
}

// A JP-region no-pick resolve queries by the translit form and lands
// the Super Famicom listing for a SNES pick (gate via the JP twin
// table), forking a sibling member and leaving ntsc_u resolves on the
// base listing.
func TestResolve_RegionJPLandsJPListing(t *testing.T) {
	s := newStack(t)

	// Secret of Mana (fixture 1016) on SNES (19): its ja-JP alternative
	// name "Seiken Densetsu 2" is the aligned translit pair with the
	// Super Famicom fixture listing 5101.
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1016, "platform_igdb_id": 19, "region": "ntsc_j",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("ntsc_j resolve: %d %s", resp.StatusCode, b)
	}
	jp := decodeBody[api.Product](t, resp)
	if jp.Pricecharting == nil || jp.Pricecharting.PcProductId != 5101 {
		t.Fatalf("ntsc_j must land the Super Famicom listing 5101: %+v", jp.Pricecharting)
	}
	if jp.Pricecharting.ConsoleName != "Super Famicom" {
		t.Fatalf("ntsc_j console_name: %q", jp.Pricecharting.ConsoleName)
	}

	resp = s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1016, "platform_igdb_id": 19, "region": "ntsc_u",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("ntsc_u resolve: %d %s", resp.StatusCode, b)
	}
	base := decodeBody[api.Product](t, resp)
	if base.Pricecharting == nil {
		t.Fatalf("ntsc_u must land the base listing: %+v", base.Pricecharting)
	}
	if base.Pricecharting.ConsoleName != "Super Nintendo" {
		t.Fatalf("ntsc_u console_name: %q", base.Pricecharting.ConsoleName)
	}
	if base.Pricecharting.PcProductId == jp.Pricecharting.PcProductId || base.Id == jp.Id {
		t.Fatalf("ntsc_j and ntsc_u must fork distinct sibling members: %+v vs %+v", base.Pricecharting, jp.Pricecharting)
	}
}

// A pal resolve with no PAL listing in the provider stays unmatched -
// strict class acceptance never falls back to the NA listing.
func TestResolve_RegionPALWithoutListingStaysUnmatched(t *testing.T) {
	s := newStack(t)

	// Chrono Trigger (fixture 1011) on SNES (19) has a base listing
	// (5011) but no PAL row anywhere in the fixtures.
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19, "region": "pal",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("pal resolve: %d %s", resp.StatusCode, b)
	}
	p := decodeBody[api.Product](t, resp)
	if p.Pricecharting != nil {
		t.Fatalf("pal resolve without a PAL listing must stay unmatched: %+v", p.Pricecharting)
	}
}

// Region is a matching input only: the picker path ignores it, and an
// unknown region value behaves as base.
func TestResolve_RegionIgnoredOnPickerPathAndUnknownIsBase(t *testing.T) {
	s := newStack(t)

	// Picker path: the chosen listing wins regardless of region.
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19, "pc_product_id": 5011, "region": "ntsc_j",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("picker resolve: %d %s", resp.StatusCode, b)
	}
	picked := decodeBody[api.Product](t, resp)
	if picked.Pricecharting == nil || picked.Pricecharting.PcProductId != 5011 {
		t.Fatalf("the picked listing must win regardless of region: %+v", picked.Pricecharting)
	}

	// Unknown region value: byte-equal to a regionless resolve of the
	// same identity. A throwaway resolve creates the product first, so
	// both calls below take the find path - a fresh create's in-memory
	// timestamps are never byte-identical to a found doc's Mongo-
	// round-tripped ones, regardless of region, so comparing a create
	// against a find would prove nothing about region.
	warm := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1016, "platform_igdb_id": 19,
	})
	if warm.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(warm.Body)
		t.Fatalf("warm resolve: %d %s", warm.StatusCode, b)
	}
	_, _ = io.ReadAll(warm.Body)
	_ = warm.Body.Close()

	respNone := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1016, "platform_igdb_id": 19,
	})
	if respNone.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respNone.Body)
		t.Fatalf("regionless resolve: %d %s", respNone.StatusCode, b)
	}
	noneBody, err := io.ReadAll(respNone.Body)
	_ = respNone.Body.Close()
	if err != nil {
		t.Fatal(err)
	}

	respUnknown := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1016, "platform_igdb_id": 19, "region": "someday_region",
	})
	if respUnknown.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(respUnknown.Body)
		t.Fatalf("unknown-region resolve: %d %s", respUnknown.StatusCode, b)
	}
	unknownBody, err := io.ReadAll(respUnknown.Body)
	_ = respUnknown.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(noneBody, unknownBody) {
		t.Fatalf("unknown region must behave byte-identically to no region:\n%s\nvs\n%s", noneBody, unknownBody)
	}
}

// The fallback leg: a JP-region resolve whose translit query surfaces
// nothing the gate admits re-searches by the canonical name and finds
// the hybrid-named JP listing; the second leg rides the pc_listing
// search cache.
func TestResolve_RegionFallbackSearchFindsHybridListing(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// Mega Man 2 (fixture 1024) on NES (18): its ja-JP romanization
	// "Rockman 2" matches no fixture listing, but the canonical name
	// "Mega Man 2" hits the Famicom-console fixture 5105 that the
	// ntsc_j gate admits for an NES pick.
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1024, "platform_igdb_id": 18, "region": "ntsc_j",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("resolve: %d %s", resp.StatusCode, b)
	}
	p := decodeBody[api.Product](t, resp)
	if p.Pricecharting == nil || p.Pricecharting.PcProductId != 5105 {
		t.Fatalf("fallback leg must land the Famicom listing 5105: %+v", p.Pricecharting)
	}
	if p.Pricecharting.ConsoleName != "Famicom" {
		t.Fatalf("console_name: %q", p.Pricecharting.ConsoleName)
	}

	for _, q := range []string{"Rockman 2", "Mega Man 2"} {
		body, err := s.h.cache.GetSearch(ctx, "pc_listing", normQuery(q))
		if err != nil {
			t.Fatalf("GetSearch(%q): %v", q, err)
		}
		if body == nil {
			t.Fatalf("both legs must ride the pc_listing search cache; %q is missing", q)
		}
	}
}

// TestResolve_HealsPreFeatureRawReleaseDates pins gamePayloadFor's
// self-heal: a raw doc predating this feature (no release_dates key on
// the stored game subdocument at all, decoding to a nil Go slice) does
// not satisfy the read, so one provider refetch repairs it and the
// healed rows reach the resolved product. A raw doc UpsertRaw already
// normalized to the empty-but-fetched marker satisfies the read and
// skips the provider entirely.
func TestResolve_HealsPreFeatureRawReleaseDates(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	const platformID = 8801

	// Bypass UpsertRaw's normalization: insert the raw doc through the
	// Mongo handle directly, omitting release_dates so it decodes as
	// nil rather than the empty-array fetched-but-none marker. Every
	// other field (platforms included) is what a real pre-feature
	// payload would already carry - release_dates is the only new one.
	const staleGameID = 93340
	if _, err := s.mdb.Collection("igdb_raw").InsertOne(ctx, bson.M{
		"_id": staleGameID,
		"game": bson.M{
			"id":        staleGameID,
			"name":      "Pre-Feature Game",
			"platforms": []bson.M{{"id": platformID, "name": "Test Platform"}},
		},
		"fetched_at": time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	var calls int
	healedDate := time.Date(2001, time.June, 20, 0, 0, 0, 0, time.UTC)
	s.h.games = &stubGames{
		gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
			calls++
			return []igdb.Game{{
				ID:        staleGameID,
				Name:      "Pre-Feature Game",
				Platforms: []igdb.Named{{ID: platformID, Name: "Test Platform"}},
				ReleaseDates: []igdb.ReleaseDate{
					{Date: healedDate.Unix(), Platform: platformID, Region: 5}, // japan
				},
			}}, nil
		},
		// The stack's platform catalog is cold on a fresh container;
		// resolve's platformLogoFor triggers one refresh (best-effort,
		// unrelated to this test). A non-empty answer lets UpsertPlatforms
		// stamp fetched_at so the catalog reads warm from then on -
		// otherwise every resolve call re-triggers this same provider
		// call, and the counter-case below overrides games with a stub
		// that has no platforms field.
		platforms: func(context.Context) ([]igdb.Platform, error) {
			return []igdb.Platform{{ID: platformID, Name: "Test Platform"}}, nil
		},
	}

	p := s.resolveGame(staleGameID, platformID)
	if calls != 1 {
		t.Fatalf("nil-table raw must trigger exactly one refetch, got %d calls", calls)
	}
	if p.Igdb == nil || p.Igdb.ReleaseDates == nil || len(*p.Igdb.ReleaseDates) != 1 {
		t.Fatalf("healed release_dates missing: %+v", p.Igdb)
	}
	rd := (*p.Igdb.ReleaseDates)[0]
	if rd.Region != "japan" || !rd.Date.Equal(healedDate) {
		t.Fatalf("healed release_date row: %+v", rd)
	}

	// Counter-case: a raw doc UpsertRaw already normalized (fetched,
	// IGDB listed no dated rows for the platform) must NOT refetch.
	const freshGameID = 93341
	if err := s.store.UpsertRaw(ctx, []igdb.Game{{
		ID:           freshGameID,
		Name:         "Fetched But None",
		Platforms:    []igdb.Named{{ID: platformID, Name: "Test Platform"}},
		ReleaseDates: []igdb.ReleaseDate{},
	}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	// Seed the platform catalog directly instead of leaning on the
	// healed case above having already warmed it through its
	// games.platforms stub: this case must hold on its own (e.g. if it
	// were the only one to run), and resolveGame's platformLogoFor call
	// would otherwise hit a cold catalog and reach for h.games.Platforms
	// below, which panics.
	if err := s.store.UpsertPlatforms(ctx, []igdb.Platform{{ID: platformID, Name: "Test Platform"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.h.games = &stubGames{} // panics if GamesByIDs or Platforms is called
	p2 := s.resolveGame(freshGameID, platformID)
	if p2.Igdb == nil || p2.Igdb.GameId != freshGameID {
		t.Fatalf("fetched-but-none raw must resolve without a refetch: %+v", p2.Igdb)
	}
	if p2.Igdb.ReleaseDates != nil {
		t.Fatalf("no dated rows for the platform must serve release_dates absent: %+v", *p2.Igdb.ReleaseDates)
	}
}

// TestResolve_HealsBelowVersionRaw pins gamePayloadFor's version-based
// heal: a raw doc that already carries a real release table (so the
// nil-table check alone would treat it as fetched) but predates
// fields_version tracking - and the localization arrays that generation
// added - is still a miss. One refetch repairs it and the healed
// localizations reach the minted product.
func TestResolve_HealsBelowVersionRaw(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	const gid = 424243
	const platformID = 8802
	naDate := time.Date(2003, time.September, 9, 0, 0, 0, 0, time.UTC)

	// Hand-write the raw doc: release_dates is real (the nil-table case
	// is TestResolve_HealsPreFeatureRawReleaseDates's job), but
	// fields_version and the localization arrays a newer generation added
	// are absent - the below-version case this test exists for.
	if _, err := s.mdb.Collection("igdb_raw").InsertOne(ctx, bson.M{
		"_id": gid,
		"game": bson.M{
			"id":            gid,
			"name":          "Regional Quest II",
			"platforms":     []bson.M{{"id": platformID, "name": "Test Platform"}},
			"release_dates": []bson.M{{"date": naDate.Unix(), "platform": platformID, "release_region": 2}},
		},
		"fetched_at": time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	// Pre-warm the platform catalog: resolve's platformLogoFor otherwise
	// reaches for h.games.Platforms below, which this stub does not
	// carry (matching the counter-case in HealsPreFeatureRawReleaseDates).
	if err := s.store.UpsertPlatforms(ctx, []igdb.Platform{{ID: platformID, Name: "Test Platform"}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	var calls int
	s.h.games = &stubGames{gamesByIDs: func(_ context.Context, ids []int64) ([]igdb.Game, error) {
		calls++
		if len(ids) != 1 || ids[0] != gid {
			t.Fatalf("unexpected refetch ids: %v", ids)
		}
		return []igdb.Game{{
			ID:        gid,
			Name:      "Regional Quest II",
			Platforms: []igdb.Named{{ID: platformID, Name: "Test Platform"}},
			ReleaseDates: []igdb.ReleaseDate{
				{Date: naDate.Unix(), Platform: platformID, Region: 2}, // north_america
			},
			GameLocalizations: []igdb.GameLocalization{
				{Name: "地域限定クエスト", Region: igdb.LocalizationRegion{Identifier: "ja-JP"}},
			},
		}}, nil
	}}

	p := s.resolveGame(gid, platformID)
	if calls != 1 {
		t.Fatalf("below-version raw must trigger exactly one refetch, got %d", calls)
	}
	if p.Igdb == nil || p.Igdb.Localizations == nil || len(*p.Igdb.Localizations) != 1 {
		t.Fatalf("minted product must carry the refetched localizations: %+v", p.Igdb)
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

// Listing-keyed identity end to end: no-pick resolves converge on the
// auto-matched member; a manual pick of a DIFFERENT listing is a
// distinct member of the same family; the same pick converges; and
// request region/edition/variant are ignored for games.
func TestResolve_GameConvergesPerListing(t *testing.T) {
	s := newStack(t)

	auto1 := s.resolveGame(1005, 4)
	auto2 := s.resolveGame(1005, 4)
	if auto1.Id != auto2.Id {
		t.Fatalf("no-pick resolves must converge: %s vs %s", auto1.Id, auto2.Id)
	}
	if auto1.Pricecharting == nil || auto1.Pricecharting.PcProductId != 5005 {
		t.Fatalf("auto-match must land the base listing: %+v", auto1.Pricecharting)
	}

	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1005, "platform_igdb_id": 4,
		"pc_product_id": 5099, "variant": "players choice cart",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("picker resolve: %d", resp.StatusCode)
	}
	picked := decodeBody[api.Product](t, resp)
	if picked.Id == auto1.Id {
		t.Fatal("a different listing must be a distinct member")
	}
	if picked.Pricecharting == nil || picked.Pricecharting.PcProductId != 5099 ||
		picked.Pricecharting.MatchConfidence != 1.0 || picked.Pricecharting.Verified {
		t.Fatalf("picked member mapping: %+v", picked.Pricecharting)
	}
	if picked.Igdb == nil || auto1.Igdb == nil || picked.Igdb.GameId != auto1.Igdb.GameId {
		t.Fatal("members must share the igdb family")
	}

	resp = s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1005, "platform_igdb_id": 4, "pc_product_id": 5099,
	})
	again := decodeBody[api.Product](t, resp)
	if again.Id != picked.Id {
		t.Fatalf("same pick must converge: %s vs %s", again.Id, picked.Id)
	}
}

// A hint the candidates cannot carry keeps the resolve conservative:
// it lands on the family's single unmatched member (both times), and
// the plain resolve still lands on the matched member beside it.
func TestResolve_MatchHintBelowThresholdLandsUnmatchedMember(t *testing.T) {
	s := newStack(t)

	body := map[string]any{
		"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19,
		"match_hint": "grey cart brick serial",
	}
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("hinted resolve: %d", resp.StatusCode)
	}
	miss1 := decodeBody[api.Product](t, resp)
	if miss1.Pricecharting != nil {
		t.Fatalf("junk hint must not guess: %+v", miss1.Pricecharting)
	}
	resp = s.do(http.MethodPost, "/products/resolve", s.userToken(), body)
	miss2 := decodeBody[api.Product](t, resp)
	if miss2.Id != miss1.Id {
		t.Fatalf("unmatched member must be the family singleton: %s vs %s", miss2.Id, miss1.Id)
	}

	matched := s.resolveGame(1011, 19)
	if matched.Id == miss1.Id || matched.Pricecharting == nil || matched.Pricecharting.PcProductId != 5011 {
		t.Fatalf("plain resolve must land the matched member beside it: %+v", matched)
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
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		// gamePayloadFor checks igdb_raw first; an empty read falls
		// through to the provider, which is the one erroring here.
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
	}
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

// A pre-feature raw (present, nil release table) with the provider down
// is a usable stale payload: gamePayloadFor serves it - the projection
// just misses per-region dates - rather than erroring, matching the
// read path's serve-stale posture. The nightly reprojection refetches
// nil-table raws, so the minted product heals on the next refresh. This is
// the counterpart to TestUnitResolve_UpstreamDown (no raw -> the error
// stands).
func TestUnitResolve_PreFeatureRawServesStaleWhenProviderDown(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	stale := igdb.Game{
		ID: 1011, Name: "Chrono Trigger",
		Platforms: []igdb.Named{{ID: 19, Name: "Super Nintendo Entertainment System"}},
		// ReleaseDates nil: the pre-feature sentinel.
	}
	var created store.Product
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) {
			return []store.RawGame{{GameID: 1011, Game: stale, FetchedAt: time.Unix(1000, 0).UTC()}}, nil
		},
		// upsertRaw left nil: serving stale must not refetch or re-upsert.
		createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
			p.ID = "77777777-7777-7777-7777-777777777777"
			created = p
			return p, nil
		},
		platformsFetchedAt: func(context.Context) (time.Time, error) { return time.Now(), nil },
		listPlatforms: func(context.Context) ([]store.CatalogPlatform, error) {
			return []store.CatalogPlatform{{ID: 19, Name: "Super Nintendo Entertainment System"}}, nil
		},
	}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return nil, errors.New("igdb down") // refetch fails; the stale raw must be served
	}}
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		return nil, errors.New("pricecharting down") // auto-match lands on the unmatched member
	}}
	h := newUnitHandlers(st, games, prices, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19})
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-feature raw must resolve from the stale payload when the provider is down: %d %s", rec.Code, rec.Body.String())
	}
	if created.IGDB == nil || created.IGDB.GameID != 1011 {
		t.Fatalf("stale payload must build the projection: %+v", created.IGDB)
	}
	if !created.IGDB.FetchedAt.Equal(time.Unix(1000, 0).UTC()) {
		t.Fatalf("stale projection must keep the raw's own stamp: %v", created.IGDB.FetchedAt)
	}
	if len(created.IGDB.ReleaseDates) != 0 {
		t.Fatalf("a pre-feature stale payload has no dated rows: %+v", created.IGDB.ReleaseDates)
	}
}

// TestUnitResolve_BelowVersionRawServesStaleWhenProviderDown pins the
// stale-serve arm for the new miss reason: a raw already carrying a
// real release table (the nil-table check alone would treat it as a
// hit) but below fields_version is still a miss, and when the provider
// is down for the repair attempt the existing stale-serve arm must
// still serve it rather than fail resolve outright.
func TestUnitResolve_BelowVersionRawServesStaleWhenProviderDown(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	stale := igdb.Game{
		ID:        1011,
		Name:      "Chrono Trigger",
		Platforms: []igdb.Named{{ID: 19, Name: "Super Nintendo Entertainment System"}},
		ReleaseDates: []igdb.ReleaseDate{
			{Date: 809049600, Platform: 19, Region: 2}, // real table: not the nil-table case
		},
		// FieldsVersion left at the RawGame literal's zero value below:
		// below store.RawFieldsVersion, the case under test.
	}
	var created store.Product
	st := &stubStore{
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) {
			return []store.RawGame{{GameID: 1011, Game: stale, FetchedAt: time.Unix(1000, 0).UTC()}}, nil
		},
		// upsertRaw left nil: serving stale must not refetch or re-upsert.
		createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
			p.ID = "88888888-8888-8888-8888-888888888888"
			created = p
			return p, nil
		},
		platformsFetchedAt: func(context.Context) (time.Time, error) { return time.Now(), nil },
		listPlatforms: func(context.Context) ([]store.CatalogPlatform, error) {
			return []store.CatalogPlatform{{ID: 19, Name: "Super Nintendo Entertainment System"}}, nil
		},
	}
	var calls int
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		calls++
		return nil, errors.New("igdb down") // refetch fails; the stale raw must be served
	}}
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		return nil, errors.New("pricecharting down") // auto-match lands on the unmatched member
	}}
	h := newUnitHandlers(st, games, prices, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 1011, "platform_igdb_id": 19})
	if rec.Code != http.StatusOK {
		t.Fatalf("below-version raw must resolve from the stale payload when the provider is down: %d %s", rec.Code, rec.Body.String())
	}
	// The differentiator from the pre-existing hit path: a below-version
	// raw with a real release table must still attempt (and fail) a
	// refetch, not serve straight from the read like an already-current
	// raw would. Without the version check this raw satisfies the old
	// nil-table-only hit condition and calls never fires.
	if calls != 1 {
		t.Fatalf("below-version raw must attempt exactly one refetch before falling back to stale, got %d", calls)
	}
	if created.IGDB == nil || created.IGDB.GameID != 1011 {
		t.Fatalf("stale payload must build the projection: %+v", created.IGDB)
	}
	if !created.IGDB.FetchedAt.Equal(time.Unix(1000, 0).UTC()) {
		t.Fatalf("stale projection must keep the raw's own stamp: %v", created.IGDB.FetchedAt)
	}
	if len(created.IGDB.ReleaseDates) == 0 {
		t.Fatalf("a below-version stale payload still carries its real release rows: %+v", created.IGDB.ReleaseDates)
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
		rawByIDs:  func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
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
	// The null member carries no region/edition/variant: those are
	// vestigial on games, not part of the (game, platform, listing) key.
	if created.Region != "" || created.Edition != "" || created.Variant != "" {
		t.Fatalf("unmatched member must not be a variant-keyed tuple: %+v", created)
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
		rawByIDs:  func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
		upsertRaw: func(context.Context, []igdb.Game, time.Time) error { return nil },
		createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
			passedID = p.ID
			// Lost race: the winner's doc comes back under another id.
			// Listing-keyed identity means the loser and the winner
			// scored the same auto-match before racing to create, so
			// the winner's doc already carries that same mapping.
			winner := p
			winner.ID = "77777777-7777-7777-7777-777777777777"
			return winner, nil
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
		rawByIDs:      func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
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
		rawByIDs:      func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
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
	st := &stubStore{findProduct: notFound,
		rawByIDs:           func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
		upsertRaw:          func(context.Context, []igdb.Game, time.Time) error { return nil },
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
	noRaw := &stubStore{findProduct: notFound,
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil }}
	rec = serveUnit(t, newUnitHandlers(noRaw, noGames, &stubPrices{}, newStubCache()),
		env, http.MethodPost, "/products/resolve", tok,
		map[string]any{"type": "game", "igdb_game_id": 999999, "platform_igdb_id": 19, "pc_product_id": 7042})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown_game") {
		t.Fatalf("unknown game must win: %d %s", rec.Code, rec.Body.String())
	}
}

// The hint reweights scoring (score-only): with unbracketed variant
// candidates, the hint flips the winner and the stored confidence is
// the score, not 1.0. The variant candidate carries an extra token
// ("cartridge") the hint alone does not cover, so the winning score
// is a genuine partial match rather than a coincidental 1.0 - unlike
// a manual match, which always stores exactly 1.0 by construction.
func TestUnitResolve_MatchHintFlipsTheWinner(t *testing.T) {
	env := newAuthEnv(t)
	game := igdb.Game{ID: 1005, Name: "Super Mario 64", Platforms: []igdb.Named{{ID: 4, Name: "Nintendo 64"}}}
	loose := int64(3500)
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		return []pricecharting.Product{
			{ID: 901, Name: "Super Mario 64", ConsoleName: "Nintendo 64", LoosePriceCents: &loose},
			{ID: 902, Name: "Super Mario 64 Players Choice Cartridge", ConsoleName: "Nintendo 64", LoosePriceCents: &loose},
		}, nil
	}}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) { return []igdb.Game{game}, nil }}
	var created store.Product
	st := &stubStore{
		rawByIDs:           func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
		upsertRaw:          func(context.Context, []igdb.Game, time.Time) error { return nil },
		platformsFetchedAt: func(context.Context) (time.Time, error) { return time.Now(), nil },
		listPlatforms:      func(context.Context) ([]store.CatalogPlatform, error) { return nil, nil },
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
			created = p
			return p, nil
		},
		appendSnapshot: func(context.Context, store.Snapshot) error { return nil },
	}
	h := newUnitHandlers(st, games, prices, newStubCache())
	tok := env.token(t, "u1", []string{"user"})

	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok, map[string]any{
		"type": "game", "igdb_game_id": 1005, "platform_igdb_id": 4,
		"match_hint": "players choice",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.Bytes())
	}
	if created.PriceCharting == nil || created.PriceCharting.PCProductID != 902 {
		t.Fatalf("hint must flip the winner: %+v", created.PriceCharting)
	}
	if created.PriceCharting.MatchConfidence >= 1.0 || created.PriceCharting.Verified {
		t.Fatalf("score-only hint must store the score unverified: %+v", created.PriceCharting)
	}
}

// The resolve reads and populates the SAME cache entries the
// pc_listing search endpoint serves: one provider search feeds both.
func TestUnitResolve_SharesThePCListingSearchCache(t *testing.T) {
	env := newAuthEnv(t)
	game := igdb.Game{ID: 1005, Name: "Super Mario 64", Platforms: []igdb.Named{{ID: 4, Name: "Nintendo 64"}}}
	loose := int64(3500)
	var searchCalls int
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		searchCalls++
		return []pricecharting.Product{{ID: 5005, Name: "Super Mario 64", ConsoleName: "Nintendo 64", LoosePriceCents: &loose}}, nil
	}}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) { return []igdb.Game{game}, nil }}
	st := &stubStore{
		rawByIDs:           func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
		upsertRaw:          func(context.Context, []igdb.Game, time.Time) error { return nil },
		platformsFetchedAt: func(context.Context) (time.Time, error) { return time.Now(), nil },
		listPlatforms:      func(context.Context) ([]store.CatalogPlatform, error) { return nil, nil },
		findProduct: func(context.Context, store.ProductKey) (store.Product, error) {
			return store.Product{}, store.ErrNotFound
		},
		createProduct:  func(_ context.Context, p store.Product) (store.Product, error) { return p, nil },
		appendSnapshot: func(context.Context, store.Snapshot) error { return nil },
	}
	c := newStubCache()
	h := newUnitHandlers(st, games, prices, c)
	tok := env.token(t, "u1", []string{"user"})

	rec := serveUnit(t, h, env, http.MethodPost, "/products/resolve", tok, map[string]any{
		"type": "game", "igdb_game_id": 1005, "platform_igdb_id": 4,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.Bytes())
	}
	if searchCalls != 1 {
		t.Fatalf("resolve must search the provider once, got %d", searchCalls)
	}
	if c.search["pc_listing:super mario 64"] == nil {
		t.Fatal("resolve must populate the pc_listing search cache")
	}

	// The search endpoint now serves the resolve-populated entry.
	rec = serveUnit(t, h, env, http.MethodGet, "/search?type=pc_listing&q=Super%20Mario%2064", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search status %d", rec.Code)
	}
	if searchCalls != 1 {
		t.Fatalf("search endpoint must hit the shared cache, provider calls %d", searchCalls)
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

// waitFor polls until check passes (the catalog refresh is detached).
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

// doInternal drives the CronJob path: a Bearer service token instead
// of a user's own.
func (s *stack) doInternal(bearer string) *http.Response {
	s.t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.srv.URL+"/internal/refresh", nil)
	if err != nil {
		s.t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp
}

// serveInternal is the unit-layer equivalent of doInternal.
func serveInternal(t *testing.T, h *Handlers, env *authEnv, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/internal/refresh", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
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
	// One unmatched product: the refresh must skip it.
	_ = s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1018, "platform_igdb_id": 19,
	}).Body.Close()

	resp := s.doInternal(s.serviceToken()) // service token: the CronJob path
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("internal refresh: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	ctx := context.Background()
	waitFor(t, 10*time.Second, func() bool {
		// Each matched product got its resolve-time snapshot plus one
		// refresh snapshot; Terranigma got none.
		n, err := s.mdb.Collection("price_snapshots").CountDocuments(ctx, map[string]any{})
		return err == nil && n == 4
	})
	got, err := s.store.GetProduct(ctx, matched.Id.String())
	if err != nil || got.PriceCharting == nil {
		t.Fatalf("processed product: %v", err)
	}
	if got.PriceCharting.AsOf.Before(time.Now().Add(-time.Minute)) {
		t.Fatalf("as_of not refreshed: %v", got.PriceCharting.AsOf)
	}
}

// TestRefresh_WalksPCListingProducts pins that the daily refresh is not
// scoped to "game" products: ListPriced filters on the PriceCharting
// mapping existing at all, so a pc_listing price-anchor product (no
// igdb subdoc, created straight off a listing id) must be walked and
// snapshotted exactly like a resolved game.
func TestRefresh_WalksPCListingProducts(t *testing.T) {
	s := newStack(t)
	created := decodeBody[api.Product](t,
		s.do(http.MethodPost, "/products/resolve", s.userToken(),
			map[string]any{"type": "pc_listing", "pc_product_id": 5099}))

	resp := s.doInternal(s.serviceToken()) // service token: the CronJob path
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("internal refresh: %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	ctx := context.Background()
	waitFor(t, 10*time.Second, func() bool {
		// The create-time snapshot plus one refresh snapshot.
		n, err := s.mdb.Collection("price_snapshots").CountDocuments(ctx, map[string]any{})
		return err == nil && n == 2
	})

	hist := s.do(http.MethodPost, "/products/price-history:batch", s.userToken(),
		map[string]any{"product_ids": []string{created.Id.String()}})
	series := decodeBody[api.PriceHistoryResponse](t, hist).Series
	if len(series[created.Id.String()]) < 2 {
		t.Fatalf("refresh must snapshot pc_listing products: got %d points", len(series[created.Id.String()]))
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

// TestUnitInternalRefresh_RequiresServiceToken pins the guard that
// replaced the retired X-Internal-Token check: a bearer-less request
// never reaches the handler (jwtauth 401s first, see
// TestRoutes_InternalRefreshRequiresBearer); a plain user's own
// access token clears jwtauth but is forbidden by requireService; an
// ADMIN token is forbidden too - requireService is service-only, the
// distinguishing case from requireAdminOrService (collection's guard
// on its admin-or-service levers), so this pins that swapping one
// for the other here would not go unnoticed; a minted service token
// (token_use=service) is accepted and 202s.
func TestUnitInternalRefresh_RequiresServiceToken(t *testing.T) {
	env := newAuthEnv(t)
	h := New(&stubStore{
		listPriced:       func(context.Context) ([]store.Product, error) { return nil, nil },
		listIGDBProducts: func(context.Context) ([]store.Product, error) { return nil, nil },
	},
		nil, nil, nil, newStubCache(), Options{
			Logger: slog.New(slog.DiscardHandler),
		})

	rec := serveInternal(t, h, env, env.token(t, "11111111-1111-1111-1111-111111111111", []string{"user"}))
	if rec.Code != http.StatusForbidden || !bytes.Contains(rec.Body.Bytes(), []byte("forbidden")) {
		t.Fatalf("plain user token: %d %s", rec.Code, rec.Body.String())
	}

	rec = serveInternal(t, h, env, env.token(t, "22222222-2222-2222-2222-222222222222", []string{"user", "admin"}))
	if rec.Code != http.StatusForbidden || !bytes.Contains(rec.Body.Bytes(), []byte("forbidden")) {
		t.Fatalf("admin token must also be refused (service-only guard): %d %s", rec.Code, rec.Body.String())
	}

	rec = serveInternal(t, h, env, env.serviceToken(t, "svc:catalog-refresh"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("service token: %d %s", rec.Code, rec.Body.String())
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
}

func TestUnitRefresh_ConflictWhileRunning(t *testing.T) {
	env := newAuthEnv(t)
	release := make(chan struct{})
	started := make(chan struct{})
	st := &stubStore{
		listPriced: func(context.Context) ([]store.Product, error) {
			close(started)
			<-release
			return nil, nil
		},
		listIGDBProducts: func(context.Context) ([]store.Product, error) { return nil, nil },
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	tok := env.serviceToken(t, "svc:catalog-refresh")

	rec := serveInternal(t, h, env, tok)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first trigger: %d", rec.Code)
	}
	<-started
	rec = serveInternal(t, h, env, tok)
	if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("refresh_in_progress")) {
		t.Fatalf("concurrent trigger: %d %s", rec.Code, rec.Body.String())
	}
	close(release)
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })

	// The guard resets: a third trigger is accepted again.
	st.listPriced = func(context.Context) ([]store.Product, error) { return nil, nil }
	rec = serveInternal(t, h, env, tok)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post-refresh trigger: %d", rec.Code)
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
}

func TestUnitRefresh_RefreshSurvivesPerProductFailures(t *testing.T) {
	env := newAuthEnv(t)
	loose := int64(1000)
	prods := []store.Product{
		{ID: "aaaaaaaa-0000-0000-0000-000000000001", PriceCharting: &store.PCMeta{PCProductID: 1}},
		{ID: "aaaaaaaa-0000-0000-0000-000000000002", PriceCharting: &store.PCMeta{PCProductID: 2}},
	}
	var snaps int
	st := &stubStore{
		listPriced:       func(context.Context) ([]store.Product, error) { return prods, nil },
		listIGDBProducts: func(context.Context) ([]store.Product, error) { return nil, nil },
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

	rec := serveInternal(t, h, env, env.serviceToken(t, "svc:catalog-refresh"))
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
			// The budget expires partway through the refresh (after the
			// 2nd of 5 products): the next iteration's ctx.Err() check
			// must stop the refresh instead of visiting the rest.
			cancel()
		}
		return pricecharting.Product{ID: 1, Name: "P", ConsoleName: "C"}, nil
	}}
	h := newUnitHandlers(st, nil, prices, newStubCache())

	h.runRefresh(ctx)

	if calls != 2 {
		t.Fatalf("refresh must stop between products once ctx is done: price provider called %d times, want 2", calls)
	}
}

func TestUnitRefresh_RefreshPanicIsContained(t *testing.T) {
	env := newAuthEnv(t)
	st := &stubStore{
		listPriced: func(context.Context) ([]store.Product, error) {
			panic("boom")
		},
		listIGDBProducts: func(context.Context) ([]store.Product, error) { return nil, nil },
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	tok := env.serviceToken(t, "svc:catalog-refresh")

	rec := serveInternal(t, h, env, tok)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trigger before the panicking refresh: %d", rec.Code)
	}
	// If the panic escaped the goroutine, the whole test binary would
	// already be dead here; reaching this line at all is part of the
	// proof.
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })

	// The guard reset after the panic: a second trigger is accepted
	// again, not 409 (a leaked guard would answer 409 forever).
	st.listPriced = func(context.Context) ([]store.Product, error) { return nil, nil }
	rec = serveInternal(t, h, env, tok)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post-panic trigger: %d", rec.Code)
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
}

// The reprojection's core repair: a pre-feature product (no raw
// held yet) is healed via a nil-table-raw refetch. Its SetIGDB call
// carries a non-nil release table whose scalar is the folded earliest
// date, the refetched game lands in igdb_raw via UpsertRaw, the rebuilt
// product's cache entry is invalidated immediately, and - because the
// data was freshly fetched - the projection carries `now` as its stamp.
func TestUnitReprojection_HealsPreFeatureProduct(t *testing.T) {
	prod := store.Product{
		ID: "p-preheal", Type: "game", Name: "Chrono Trigger",
		Platform: &store.Platform{IGDBID: 19, Name: "Super Nintendo Entertainment System"},
		IGDB:     &store.IGDBMeta{GameID: 1011, Name: "Chrono Trigger"},
	}
	var upsertCalled bool
	var setID string
	var setMeta store.IGDBMeta
	st := &stubStore{
		listIGDBProducts: func(context.Context) ([]store.Product, error) {
			return []store.Product{prod}, nil
		},
		// Pre-feature: no raw held yet, so the game lands in fetchIDs.
		rawByIDs:  func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
		upsertRaw: func(context.Context, []igdb.Game, time.Time) error { upsertCalled = true; return nil },
		setIGDB: func(_ context.Context, id string, m store.IGDBMeta) error {
			setID, setMeta = id, m
			return nil
		},
	}
	games := &stubGames{gamesByIDs: func(_ context.Context, ids []int64) ([]igdb.Game, error) {
		if len(ids) != 1 || ids[0] != 1011 {
			t.Fatalf("unexpected refetch ids: %v", ids)
		}
		return []igdb.Game{{
			ID: 1011, Name: "Chrono Trigger",
			ReleaseDates: []igdb.ReleaseDate{
				{Date: 794880000, Platform: 58, Region: 5}, // Super Famicom japan: earliest, folds into SNES
				{Date: 809049600, Platform: 19, Region: 2}, // SNES north_america
			},
		}}, nil
	}}
	c := newStubCache()
	c.prods[prod.ID] = []byte(`{"stale":true}`)
	h := newUnitHandlers(st, games, nil, c)
	nowT := time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return nowT }

	h.runReprojection(context.Background())

	if setID != prod.ID {
		t.Fatalf("SetIGDB must be called for the healed product, got id %q", setID)
	}
	if len(setMeta.ReleaseDates) == 0 {
		t.Fatalf("healed meta must carry a non-empty release table: %+v", setMeta)
	}
	wantEarliest := time.Unix(794880000, 0).UTC().Truncate(24 * time.Hour)
	if !setMeta.FirstReleaseDate.Equal(wantEarliest) {
		t.Fatalf("healed scalar must be the folded earliest date: %v", setMeta.FirstReleaseDate)
	}
	if !setMeta.FetchedAt.Equal(nowT) {
		t.Fatalf("a freshly fetched rebuild must carry now as its stamp: %v", setMeta.FetchedAt)
	}
	if !upsertCalled {
		t.Fatal("UpsertRaw must be called with the refetched game")
	}
	if _, cached := c.prods[prod.ID]; cached {
		t.Fatal("the rebuilt product's cache entry must be invalidated, not left to age out via TTL")
	}
}

// Three products sharing one game id, none with a raw yet, must cost
// exactly one GamesByIDs call (the distinct-ids batching), and all
// three must still get their projection rebuilt.
func TestUnitReprojection_BatchesSharedGameID(t *testing.T) {
	const shared = int64(1011)
	prods := []store.Product{
		{ID: "p1", Type: "game", Platform: &store.Platform{IGDBID: 19}, IGDB: &store.IGDBMeta{GameID: shared}},
		{ID: "p2", Type: "game", Platform: &store.Platform{IGDBID: 4}, IGDB: &store.IGDBMeta{GameID: shared}},
		{ID: "p3", Type: "game", Platform: &store.Platform{IGDBID: 7}, IGDB: &store.IGDBMeta{GameID: shared}},
	}
	var calls int
	var gotIDs []int64
	var setCalls int
	st := &stubStore{
		listIGDBProducts: func(context.Context) ([]store.Product, error) { return prods, nil },
		rawByIDs:         func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
		upsertRaw:        func(context.Context, []igdb.Game, time.Time) error { return nil },
		setIGDB:          func(context.Context, string, store.IGDBMeta) error { setCalls++; return nil },
	}
	games := &stubGames{gamesByIDs: func(_ context.Context, ids []int64) ([]igdb.Game, error) {
		calls++
		gotIDs = append([]int64{}, ids...)
		return []igdb.Game{{ID: shared, Name: "Shared Game"}}, nil
	}}
	h := newUnitHandlers(st, games, nil, newStubCache())

	h.runReprojection(context.Background())

	if calls != 1 {
		t.Fatalf("want exactly one GamesByIDs call for the shared game id, got %d", calls)
	}
	if len(gotIDs) != 1 || gotIDs[0] != shared {
		t.Fatalf("want a single distinct id in the batch, got %v", gotIDs)
	}
	if setCalls != 3 {
		t.Fatalf("all three sharing products must get a projection rebuild, got %d", setCalls)
	}
}

// A product whose game the provider no longer knows is skipped, not
// failed: no SetIGDB call (a nil stub field panics if it is), and the
// reprojection still finishes cleanly.
func TestUnitReprojection_MissingGameSkipsWithoutSetIGDB(t *testing.T) {
	prod := store.Product{ID: "p-missing", Type: "game", Platform: &store.Platform{IGDBID: 19}, IGDB: &store.IGDBMeta{GameID: 9999}}
	st := &stubStore{
		listIGDBProducts: func(context.Context) ([]store.Product, error) {
			return []store.Product{prod}, nil
		},
		rawByIDs:  func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
		upsertRaw: func(context.Context, []igdb.Game, time.Time) error { return nil },
		// setIGDB is left nil: any call panics loudly, proving the
		// missing-game product never reaches it.
	}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return nil, nil // the provider no longer knows this game
	}}
	h := newUnitHandlers(st, games, nil, newStubCache())

	h.runReprojection(context.Background()) // must return, not panic
}

// The diff gate: a product whose stored projection already equals the
// one rebuilt from its raw is left untouched - no SetIGDB, no cache
// invalidation - so a steady-state reprojection (raw unchanged) writes nothing.
func TestUnitReprojection_DiffGateSkipsUnchangedProjection(t *testing.T) {
	rawStamp := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	g := igdb.Game{
		ID: 1011, Name: "Chrono Trigger",
		ReleaseDates: []igdb.ReleaseDate{
			{Date: 794880000, Platform: 58, Region: 5},
			{Date: 809049600, Platform: 19, Region: 2},
		},
	}
	// The stored projection is exactly what the reprojection will rebuild.
	current := store.NewIGDBMeta(g, 19, rawStamp)
	prod := store.Product{ID: "p-steady", Type: "game", Platform: &store.Platform{IGDBID: 19}, IGDB: &current}
	st := &stubStore{
		listIGDBProducts: func(context.Context) ([]store.Product, error) { return []store.Product{prod}, nil },
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) {
			return []store.RawGame{{GameID: 1011, Game: g, FetchedAt: rawStamp, FieldsVersion: store.RawFieldsVersion}}, nil
		},
		// setIGDB and upsertRaw left nil: a call to either panics, proving
		// the gate wrote nothing.
	}
	c := newStubCache()
	c.prods[prod.ID] = []byte(`{"cached":true}`)
	// games passed but its raw is non-nil, so no refetch is attempted.
	h := newUnitHandlers(st, &stubGames{}, nil, c)
	h.now = func() time.Time { return time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC) }

	h.runReprojection(context.Background())

	if _, cached := c.prods[prod.ID]; !cached {
		t.Fatal("an unchanged projection must not invalidate the product cache")
	}
}

// The fold reproject: a product healed before the twin fold carries an
// unfolded projection (its japan date rides the Super Famicom platform,
// invisible then). The reprojection rebuilds it from the raw - now folding the
// twin row in - and, because the raw was not refetched, keeps the raw's
// own fetch stamp rather than bumping freshness the provider did not
// earn.
func TestUnitReprojection_FoldsTwinRowKeepingRawStamp(t *testing.T) {
	rawStamp := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	japanDate := time.Unix(794880000, 0).UTC().Truncate(24 * time.Hour)

	// The stored (unfolded) projection: only the SNES north_america row
	// was visible when this product was healed.
	unfolded := store.NewIGDBMeta(igdb.Game{
		ID: 1011, Name: "Chrono Trigger", FirstReleaseDate: 809049600,
		ReleaseDates: []igdb.ReleaseDate{{Date: 809049600, Platform: 19, Region: 2}},
	}, 19, rawStamp)
	// The raw holds the full table, japan riding the Super Famicom twin.
	full := igdb.Game{
		ID: 1011, Name: "Chrono Trigger", FirstReleaseDate: 794880000,
		ReleaseDates: []igdb.ReleaseDate{
			{Date: 794880000, Platform: 58, Region: 5}, // Super Famicom japan
			{Date: 809049600, Platform: 19, Region: 2}, // SNES north_america
		},
	}
	prod := store.Product{ID: "p-fold", Type: "game", Platform: &store.Platform{IGDBID: 19}, IGDB: &unfolded}

	var setCalled bool
	var setMeta store.IGDBMeta
	st := &stubStore{
		listIGDBProducts: func(context.Context) ([]store.Product, error) { return []store.Product{prod}, nil },
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) {
			return []store.RawGame{{GameID: 1011, Game: full, FetchedAt: rawStamp, FieldsVersion: store.RawFieldsVersion}}, nil
		},
		setIGDB: func(_ context.Context, _ string, m store.IGDBMeta) error { setCalled, setMeta = true, m; return nil },
	}
	h := newUnitHandlers(st, &stubGames{}, nil, newStubCache())
	h.now = func() time.Time { return time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC) }

	h.runReprojection(context.Background())

	if !setCalled {
		t.Fatal("a projection that gained the folded twin row must be rewritten")
	}
	if len(setMeta.ReleaseDates) != 2 {
		t.Fatalf("rebuilt projection must fold japan in beside north_america: %+v", setMeta.ReleaseDates)
	}
	var haveJapan bool
	for _, rd := range setMeta.ReleaseDates {
		if rd.Region == "japan" && rd.Date.Equal(japanDate) {
			haveJapan = true
		}
	}
	if !haveJapan {
		t.Fatalf("rebuilt projection must include the folded japan row: %+v", setMeta.ReleaseDates)
	}
	if !setMeta.FirstReleaseDate.Equal(japanDate) {
		t.Fatalf("scalar must be the folded earliest (japan): %v", setMeta.FirstReleaseDate)
	}
	if !setMeta.FetchedAt.Equal(rawStamp) {
		t.Fatalf("a raw-sourced rebuild must keep the raw's stamp, not bump it: got %v want %v", setMeta.FetchedAt, rawStamp)
	}
}

// TestReprojection_HealsBelowVersionRaw pins the reprojection's version-based
// heal: a raw doc that already carries a real release table (so the
// pre-feature nil-table check alone would miss it) but predates
// fields_version tracking - and the localization arrays that generation
// added - is still refetched. The refetch replaces it with a
// current-version raw and the rebuilt projection carries the healed
// localizations.
func TestReprojection_HealsBelowVersionRaw(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	const gid = 424242
	const platformID = 8801
	naDate := time.Date(2001, time.June, 20, 0, 0, 0, 0, time.UTC)

	// Hand-write the raw doc: release_dates is real (not the pre-feature
	// nil case the sibling reprojection test covers), but fields_version
	// and the localization arrays a newer generation added are absent - the
	// below-version case this test exists for.
	if _, err := s.mdb.Collection("igdb_raw").InsertOne(ctx, bson.M{
		"_id": gid,
		"game": bson.M{
			"id":            gid,
			"name":          "Regional Quest",
			"platforms":     []bson.M{{"id": platformID, "name": "Test Platform"}},
			"release_dates": []bson.M{{"date": naDate.Unix(), "platform": platformID, "release_region": 2}},
		},
		"fetched_at": time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	prod, err := s.store.CreateProduct(ctx, store.Product{
		Type:     "game",
		Name:     "Regional Quest",
		Platform: &store.Platform{IGDBID: platformID, Name: "Test Platform"},
		IGDB:     &store.IGDBMeta{GameID: gid, Name: "Regional Quest"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var calls int
	var gotIDs []int64
	s.h.games = &stubGames{gamesByIDs: func(_ context.Context, ids []int64) ([]igdb.Game, error) {
		calls++
		gotIDs = append([]int64{}, ids...)
		return []igdb.Game{{
			ID:        gid,
			Name:      "Regional Quest",
			Platforms: []igdb.Named{{ID: platformID, Name: "Test Platform"}},
			ReleaseDates: []igdb.ReleaseDate{
				{Date: naDate.Unix(), Platform: platformID, Region: 2}, // north_america
			},
			GameLocalizations: []igdb.GameLocalization{
				{Name: "リージョン限定版", Region: igdb.LocalizationRegion{Identifier: "ja-JP"}},
			},
		}}, nil
	}}

	s.h.runReprojection(ctx)

	if calls != 1 {
		t.Fatalf("below-version raw must trigger exactly one refetch, got %d", calls)
	}
	if len(gotIDs) != 1 || gotIDs[0] != gid {
		t.Fatalf("unexpected refetch ids: %v", gotIDs)
	}
	raws, err := s.store.RawByIDs(ctx, []int64{gid})
	if err != nil || len(raws) != 1 {
		t.Fatalf("raw read: %d, %v", len(raws), err)
	}
	if raws[0].FieldsVersion != store.RawFieldsVersion {
		t.Fatalf("healed raw must be stamped at the current fields_version: got %d want %d", raws[0].FieldsVersion, store.RawFieldsVersion)
	}
	got, err := s.store.GetProduct(ctx, prod.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IGDB == nil || len(got.IGDB.Localizations) == 0 {
		t.Fatalf("healed product must carry the refetched localizations: %+v", got.IGDB)
	}
}

// The nightly catalog refresh's second pass is wired into startRefresh: an
// internal refresh trigger must reach ListIGDBProducts, not just the
// price pass.
func TestUnitRefresh_InternalTriggerRunsReprojection(t *testing.T) {
	env := newAuthEnv(t)
	var called bool
	st := &stubStore{
		listPriced: func(context.Context) ([]store.Product, error) { return nil, nil },
		listIGDBProducts: func(context.Context) ([]store.Product, error) {
			called = true
			return nil, nil
		},
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())

	rec := serveInternal(t, h, env, env.serviceToken(t, "svc:catalog-refresh"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("trigger: %d", rec.Code)
	}
	waitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
	if !called {
		t.Fatal("startRefresh must run the reprojection pass")
	}
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

// Mapping changes are identity moves: a taken listing answers 409 on
// set, a clear that would collide with the family's unmatched member
// answers 409 too, and a successful clear sets match_hold.
func TestAdminMapping_IdentityTakenAndHold(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	matched := s.resolveGame(1005, 4) // auto-lands listing 5005
	resp := s.do(http.MethodPost, "/products/resolve", s.userToken(), map[string]any{
		"type": "game", "igdb_game_id": 1005, "platform_igdb_id": 4,
		"match_hint": "grey cart brick serial",
	})
	unmatched := decodeBody[api.Product](t, resp)

	// Set: the target listing is already carried by the matched member.
	resp = s.do(http.MethodPut, "/admin/products/"+unmatched.Id.String()+"/pricecharting",
		s.adminToken(), map[string]any{"pc_product_id": 5005})
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !bytes.Contains(body, []byte("identity_taken")) ||
		!bytes.Contains(body, []byte("already carries that listing")) {
		t.Fatalf("taken listing: want 409 identity_taken with the set-collision detail, got %d %s", resp.StatusCode, body)
	}

	// Clear: the family already has an unmatched member.
	resp = s.do(http.MethodPut, "/admin/products/"+matched.Id.String()+"/pricecharting",
		s.adminToken(), map[string]any{"pc_product_id": nil})
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !bytes.Contains(body, []byte("identity_taken")) ||
		!bytes.Contains(body, []byte("clearing would collide")) {
		t.Fatalf("colliding clear: want 409 identity_taken with the clear-collision detail, got %d %s", resp.StatusCode, body)
	}

	// A collision-free clear lands, unmaps, and holds.
	resp = s.do(http.MethodPut, "/admin/products/"+unmatched.Id.String()+"/pricecharting",
		s.adminToken(), map[string]any{"pc_product_id": 5099})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("free set: %d", resp.StatusCode)
	}
	resp = s.do(http.MethodPut, "/admin/products/"+matched.Id.String()+"/pricecharting",
		s.adminToken(), map[string]any{"pc_product_id": nil})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("free clear: %d", resp.StatusCode)
	}
	got, err := s.store.GetProduct(ctx, matched.Id.String())
	if err != nil || got.PriceCharting != nil || !got.MatchHold {
		t.Fatalf("clear must unmap and hold: %+v, %v", got, err)
	}
	// Setting a mapping lifts the hold and stays admin-verified.
	resp = s.do(http.MethodPut, "/admin/products/"+matched.Id.String()+"/pricecharting",
		s.adminToken(), map[string]any{"pc_product_id": 5005})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("re-set: %d", resp.StatusCode)
	}
	got, err = s.store.GetProduct(ctx, matched.Id.String())
	if err != nil || got.PriceCharting == nil || !got.PriceCharting.Verified || got.MatchHold {
		t.Fatalf("re-set must map verified and lift the hold: %+v, %v", got, err)
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

func TestAdminUnmatchedWorklist(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	oldest, err := s.store.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Worklist Oldest",
		Platform: &store.Platform{IGDBID: 4, Name: "Nintendo 64"},
		IGDB:     &store.IGDBMeta{GameID: 9201, Name: "Worklist Oldest", Genres: []store.Genre{}, Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{}, Companies: []store.Company{}, FetchedAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}
	held, err := s.store.CreateProduct(ctx, store.Product{
		Type: "console", Name: "Worklist Held Console", Region: "pal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetPriceCharting(ctx, held.ID, nil); err != nil { // deliberate clear = hold
		t.Fatal(err)
	}

	// Non-admin: 403 with the forbidden code.
	resp := s.do(http.MethodGet, "/admin/products/unmatched", s.userToken(), nil)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("forbidden")) {
		t.Fatalf("non-admin: %d %s", resp.StatusCode, body)
	}

	// Admin: the full envelope, oldest first, held item flagged.
	page := decodeBody[api.UnmatchedProductsPage](t,
		s.do(http.MethodGet, "/admin/products/unmatched", s.adminToken(), nil))
	if page.TotalCount != 2 || len(page.Products) != 2 {
		t.Fatalf("envelope: total=%d len=%d", page.TotalCount, len(page.Products))
	}
	if page.Products[0].Id.String() != oldest.ID || page.Products[1].Id.String() != held.ID {
		t.Fatalf("order: %v then %v", page.Products[0].Id, page.Products[1].Id)
	}
	if page.Products[0].MatchHold != nil {
		t.Fatal("plain unmatched product must not carry match_hold")
	}
	if page.Products[1].MatchHold == nil || !*page.Products[1].MatchHold {
		t.Fatal("held product must carry match_hold true")
	}

	// limit/offset slice the same order; total_count stays full.
	paged := decodeBody[api.UnmatchedProductsPage](t,
		s.do(http.MethodGet, "/admin/products/unmatched?limit=1&offset=1", s.adminToken(), nil))
	if paged.TotalCount != 2 || len(paged.Products) != 1 || paged.Products[0].Id.String() != held.ID {
		t.Fatalf("paged: total=%d %+v", paged.TotalCount, paged.Products)
	}

	// Out-of-bounds limits clamp instead of erroring: 0 clamps to the
	// minimum page (one row), an oversized value clamps to the max and
	// still answers.
	clampedLow := decodeBody[api.UnmatchedProductsPage](t,
		s.do(http.MethodGet, "/admin/products/unmatched?limit=0", s.adminToken(), nil))
	if len(clampedLow.Products) != 1 || clampedLow.TotalCount != 2 {
		t.Fatalf("limit=0 must clamp to 1: %+v", clampedLow)
	}
	clampedHigh := decodeBody[api.UnmatchedProductsPage](t,
		s.do(http.MethodGet, "/admin/products/unmatched?limit=9999", s.adminToken(), nil))
	if len(clampedHigh.Products) != 2 {
		t.Fatalf("limit=9999 must clamp and answer: %+v", clampedHigh)
	}
}

func TestAdminCommunityWorklist(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	oldest, err := s.store.CreateProduct(ctx, store.Product{Type: "game", Name: "Community Oldest", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	// Guards against a same-millisecond updated_at tie (the _id
	// tiebreak is a random UUID, not a real proxy for creation order).
	time.Sleep(5 * time.Millisecond)
	newer, err := s.store.CreateProduct(ctx, store.Product{Type: "console", Name: "Community Newer", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	// A provider product must never surface in the community listing.
	if _, err := s.store.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Community Worklist Provider Excluded",
		Platform: &store.Platform{IGDBID: 4, Name: "Nintendo 64"},
		IGDB: &store.IGDBMeta{GameID: 9601, Name: "Community Worklist Provider Excluded", Genres: []store.Genre{},
			Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{}, Companies: []store.Company{}, FetchedAt: time.Now().UTC()},
	}); err != nil {
		t.Fatal(err)
	}

	// Non-admin: 403 with the forbidden code.
	resp := s.do(http.MethodGet, "/admin/products/community", s.userToken(), nil)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("forbidden")) {
		t.Fatalf("non-admin: %d %s", resp.StatusCode, body)
	}

	// Admin: the full envelope, oldest first, provider product excluded.
	page := decodeBody[api.CommunityProductsPage](t,
		s.do(http.MethodGet, "/admin/products/community", s.adminToken(), nil))
	if page.TotalCount != 2 || len(page.Products) != 2 {
		t.Fatalf("envelope: total=%d len=%d", page.TotalCount, len(page.Products))
	}
	if page.Products[0].Id.String() != oldest.ID || page.Products[1].Id.String() != newer.ID {
		t.Fatalf("order: %v then %v", page.Products[0].Id, page.Products[1].Id)
	}
	for _, p := range page.Products {
		if p.Origin == nil || *p.Origin != "community" {
			t.Fatalf("every product must carry origin community: %+v", p)
		}
	}

	// limit/offset slice the same order; total_count stays full.
	paged := decodeBody[api.CommunityProductsPage](t,
		s.do(http.MethodGet, "/admin/products/community?limit=1&offset=1", s.adminToken(), nil))
	if paged.TotalCount != 2 || len(paged.Products) != 1 || paged.Products[0].Id.String() != newer.ID {
		t.Fatalf("paged: total=%d %+v", paged.TotalCount, paged.Products)
	}

	// Out-of-bounds limits clamp instead of erroring: 0 clamps to the
	// minimum page (one row), an oversized value clamps to the max and
	// still answers.
	clampedLow := decodeBody[api.CommunityProductsPage](t,
		s.do(http.MethodGet, "/admin/products/community?limit=0", s.adminToken(), nil))
	if len(clampedLow.Products) != 1 || clampedLow.TotalCount != 2 {
		t.Fatalf("limit=0 must clamp to 1: %+v", clampedLow)
	}
	clampedHighComm := decodeBody[api.CommunityProductsPage](t,
		s.do(http.MethodGet, "/admin/products/community?limit=9999", s.adminToken(), nil))
	if len(clampedHighComm.Products) != 2 {
		t.Fatalf("limit=9999 must clamp and answer: %+v", clampedHighComm)
	}
}

// TestAdminMapping_ConflictNamesHolder pins that both identity_taken
// arms name the product already holding the identity, so an admin can
// look the holder up instead of guessing which member has the listing.
func TestAdminMapping_ConflictNamesHolder(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	matched, err := s.store.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Holder Game",
		Platform: &store.Platform{IGDBID: 4, Name: "Nintendo 64"},
		IGDB:     &store.IGDBMeta{GameID: 9301, Name: "Holder Game", Genres: []store.Genre{}, Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{}, Companies: []store.Company{}, FetchedAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetPriceCharting(ctx, matched.ID, &store.PCMeta{
		PCProductID: 5005, PCName: "Super Mario 64", ConsoleName: "Nintendo 64",
		MatchConfidence: 1, AsOf: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	unmatched, err := s.store.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Holder Game",
		Platform: &store.Platform{IGDBID: 4, Name: "Nintendo 64"},
		IGDB:     &store.IGDBMeta{GameID: 9301, Name: "Holder Game", Genres: []store.Genre{}, Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{}, Companies: []store.Company{}, FetchedAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set collision: fixing the unmatched member to the taken listing
	// names the matched holder.
	resp := s.do(http.MethodPut, "/admin/products/"+unmatched.ID+"/pricecharting", s.adminToken(),
		map[string]any{"pc_product_id": 5005})
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !bytes.Contains(body, []byte("identity_taken")) {
		t.Fatalf("set collision: %d %s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(matched.ID)) || !bytes.Contains(body, []byte("Holder Game")) {
		t.Fatalf("set-collision detail must name the holder: %s", body)
	}

	// Clear collision: clearing the matched member while the family
	// already has an unmatched member names that member.
	resp = s.do(http.MethodPut, "/admin/products/"+matched.ID+"/pricecharting", s.adminToken(),
		map[string]any{"pc_product_id": nil})
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("clear collision: %d %s", resp.StatusCode, body)
	}
	if !bytes.Contains(body, []byte(unmatched.ID)) {
		t.Fatalf("clear-collision detail must name the unmatched member: %s", body)
	}
}

// TestAdminMapping_HoldUnmatched pins the parking lever the admin UI
// exposes on unmatched residue: PUT null on an already-unmatched
// product answers 200 with match_hold set, idempotently - no identity
// collision, because the clear changes nothing the unique index sees.
func TestAdminMapping_HoldUnmatched(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	orphan, err := s.store.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Orphan Game",
		Platform: &store.Platform{IGDBID: 4, Name: "Nintendo 64"},
		IGDB:     &store.IGDBMeta{GameID: 9302, Name: "Orphan Game", Genres: []store.Genre{}, Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{}, Companies: []store.Company{}, FetchedAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		got := decodeBody[api.Product](t, s.do(http.MethodPut,
			"/admin/products/"+orphan.ID+"/pricecharting", s.adminToken(),
			map[string]any{"pc_product_id": nil}))
		if got.MatchHold == nil || !*got.MatchHold || got.Pricecharting != nil {
			t.Fatalf("round %d: hold must set and mapping stay absent: %+v", i, got)
		}
	}
}

// TestAdminDeleteProduct pins the guarded mop end to end: RBAC, the
// unmatched-only guard, snapshot cleanup, and honest status codes.
func TestAdminDeleteProduct(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	orphan, err := s.store.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Mop Target",
		Platform: &store.Platform{IGDBID: 4, Name: "Nintendo 64"},
		IGDB:     &store.IGDBMeta{GameID: 9501, Name: "Mop Target", Genres: []store.Genre{}, Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{}, Companies: []store.Company{}, FetchedAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}
	matched, err := s.store.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Mop Survivor",
		Platform: &store.Platform{IGDBID: 4, Name: "Nintendo 64"},
		IGDB:     &store.IGDBMeta{GameID: 9502, Name: "Mop Survivor", Genres: []store.Genre{}, Themes: []string{}, Franchises: []string{}, SimilarGames: []int64{}, Companies: []store.Company{}, FetchedAt: time.Now().UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetPriceCharting(ctx, matched.ID, &store.PCMeta{
		PCProductID: 9510, PCName: "Mop Survivor", ConsoleName: "Nintendo 64",
		MatchConfidence: 1, AsOf: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// Non-admin: 403, nothing deleted.
	resp := s.do(http.MethodDelete, "/admin/products/"+orphan.ID, s.userToken(), nil)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !bytes.Contains(body, []byte("forbidden")) {
		t.Fatalf("non-admin: %d %s", resp.StatusCode, body)
	}

	// Admin on unmatched: 204 and the product is gone.
	resp = s.do(http.MethodDelete, "/admin/products/"+orphan.ID, s.adminToken(), nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := s.store.GetProduct(ctx, orphan.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("orphan must be gone, got %v", err)
	}

	// Repeat: 404 product_not_found.
	resp = s.do(http.MethodDelete, "/admin/products/"+orphan.ID, s.adminToken(), nil)
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || !bytes.Contains(body, []byte("product_not_found")) {
		t.Fatalf("second delete: %d %s", resp.StatusCode, body)
	}

	// Matched: 409 product_matched, survives.
	resp = s.do(http.MethodDelete, "/admin/products/"+matched.ID, s.adminToken(), nil)
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict || !bytes.Contains(body, []byte("product_matched")) {
		t.Fatalf("matched delete: %d %s", resp.StatusCode, body)
	}
	if _, err := s.store.GetProduct(ctx, matched.ID); err != nil {
		t.Fatalf("matched product must survive: %v", err)
	}
}

func TestUnitCreateCommunityProduct(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	user := env.token(t, uuid.NewString(), []string{"user"})

	var got store.Product
	st := &stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		got = p
		p.ID = uuid.NewString()
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		return p, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	// Role gate first: the store must never run for a plain user.
	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", user,
		map[string]any{"type": "game", "name": "Repro Alpha"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d, want 403", rec.Code)
	}

	// Validation: bad type and empty name are 400 invalid_body.
	rec = serveUnit(t, h, env, http.MethodPost, "/admin/products", admin,
		map[string]any{"type": "pc_listing", "name": "X"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad type: %d, want 400", rec.Code)
	}
	rec = serveUnit(t, h, env, http.MethodPost, "/admin/products", admin,
		map[string]any{"type": "game", "name": "   "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name: %d, want 400", rec.Code)
	}

	// The mint: origin community, facts in the community block, the
	// single edition field carried through, variant left empty.
	rec = serveUnit(t, h, env, http.MethodPost, "/admin/products", admin, map[string]any{
		"type": "game", "name": "Repro Alpha", "platform_name": "SNES",
		"region": "pal", "edition": "glow cart", "first_release_date": "1995-10-09",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	if got.Origin != "community" || got.Type != "game" || got.Name != "Repro Alpha" {
		t.Fatalf("stored product wrong: %+v", got)
	}
	if got.Variant != "" {
		t.Fatalf("variant must stay empty on community mints, got %q", got.Variant)
	}
	if got.Community == nil || got.Community.PlatformName != "SNES" ||
		got.Community.FirstReleaseDate.Format("2006-01-02") != "1995-10-09" {
		t.Fatalf("community facts wrong: %+v", got.Community)
	}
	// Region lives in the community block, not the top-level field:
	// the top-level field stays empty on community mints.
	if got.Region != "" || got.Community.Region != "pal" || got.Edition != "glow cart" {
		t.Fatalf("region/edition wrong: top=%q community=%q edition=%q", got.Region, got.Community.Region, got.Edition)
	}
	var out api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Origin == nil || string(*out.Origin) != "community" {
		t.Fatalf("response origin missing: %+v", out.Origin)
	}
	if out.Community == nil || out.Community.PlatformName == nil || *out.Community.PlatformName != "SNES" {
		t.Fatalf("response community block missing: %+v", out.Community)
	}
}

// TestUnitCreateCommunityProduct_MinimalBodyOmitsCommunityBlock pins the
// type+name-only mint: with neither platform_name nor
// first_release_date present, the handler must leave Community nil
// (not a zero-valued block), and the response must omit it entirely
// rather than emit an empty community object.
func TestUnitCreateCommunityProduct_MinimalBodyOmitsCommunityBlock(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})

	var got store.Product
	st := &stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		got = p
		p.ID = uuid.NewString()
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		return p, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", admin,
		map[string]any{"type": "game", "name": "Repro Minimal"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	if got.Community != nil {
		t.Fatalf("no platform_name/first_release_date must leave Community nil, got %+v", got.Community)
	}
	var out api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Community != nil {
		t.Fatalf("response must omit the community block entirely, got %+v", out.Community)
	}
}

// TestUnitCreateCommunityProduct_WhitespaceRegionOmitsCommunityBlock
// pins the hasRegion gate against a whitespace-only region: the gate
// must trim before checking, or " " (not equal to "") would slip
// through and mint an otherwise-empty community block that exists for
// no visible reason.
func TestUnitCreateCommunityProduct_WhitespaceRegionOmitsCommunityBlock(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})

	var got store.Product
	st := &stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		got = p
		p.ID = uuid.NewString()
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		return p, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", admin,
		map[string]any{"type": "game", "name": "Blank Region", "region": "   "})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	if got.Community != nil {
		t.Fatalf("a whitespace-only region alone must leave Community nil, got %+v", got.Community)
	}
	var out api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Community != nil {
		t.Fatalf("response must omit the community block entirely, got %+v", out.Community)
	}
}

// TestUnitCreateCommunityProduct_WhitespaceRegionWithOtherFact pins
// the other side of the same gate: a whitespace-only region alongside
// a real community fact still builds the block (platform_name alone
// earns it), but the region itself must land empty rather than
// storing the untrimmed whitespace.
func TestUnitCreateCommunityProduct_WhitespaceRegionWithOtherFact(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})

	var got store.Product
	st := &stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		got = p
		p.ID = uuid.NewString()
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		return p, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", admin, map[string]any{
		"type": "game", "name": "Blank Region With Platform", "platform_name": "SNES", "region": "   ",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	if got.Community == nil || got.Community.PlatformName != "SNES" {
		t.Fatalf("community facts wrong: %+v", got.Community)
	}
	if got.Community.Region != "" {
		t.Fatalf("whitespace-only region must not persist, got %q", got.Community.Region)
	}
	var out api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Community == nil || out.Community.Region != nil {
		t.Fatalf("response region must stay absent, got %+v", out.Community)
	}
}

// TestUnitCreateCommunityProduct_WhitespacePlatformNameOmitsCommunityBlock
// pins the hasPlatformName gate against a whitespace-only platform_name: the
// gate must trim before checking, or "  " (not equal to "") would slip
// through and mint an otherwise-empty community block that exists for
// no visible reason.
func TestUnitCreateCommunityProduct_WhitespacePlatformNameOmitsCommunityBlock(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})

	var got store.Product
	st := &stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		got = p
		p.ID = uuid.NewString()
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		return p, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", admin,
		map[string]any{"type": "game", "name": "Blank Platform", "platform_name": "   "})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	if got.Community != nil {
		t.Fatalf("a whitespace-only platform_name alone must leave Community nil, got %+v", got.Community)
	}
	var out api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Community != nil {
		t.Fatalf("response must omit the community block entirely, got %+v", out.Community)
	}
}

// TestUnitCreateCommunityProduct_WhitespacePlatformNameWithOtherFact pins
// the other side of the same gate: a whitespace-only platform_name alongside
// a real community fact still builds the block (region alone earns it), but
// the platform_name itself must land empty rather than storing the untrimmed
// whitespace.
func TestUnitCreateCommunityProduct_WhitespacePlatformNameWithOtherFact(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})

	var got store.Product
	st := &stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		got = p
		p.ID = uuid.NewString()
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		return p, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", admin, map[string]any{
		"type": "game", "name": "Blank Platform With Region", "platform_name": "   ", "region": "NTSC-U",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	if got.Community == nil || got.Community.Region != "NTSC-U" {
		t.Fatalf("community facts wrong: %+v", got.Community)
	}
	if got.Community.PlatformName != "" {
		t.Fatalf("whitespace-only platform_name must not persist, got %q", got.Community.PlatformName)
	}
	var out api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Community == nil || out.Community.PlatformName != nil {
		t.Fatalf("response platform_name must stay absent, got %+v", out.Community)
	}
}

// TestUnitCreateCommunityProduct_MalformedBodyIs400 mirrors the
// bad-body idiom in TestUnitBatchPrices_CapAndBadBody: serveUnit's
// body param always marshals valid JSON, so a deliberately malformed
// payload needs a raw request built by hand.
func TestUnitCreateCommunityProduct_MalformedBodyIs400(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	// createProduct left nil: a malformed body must 400 before the
	// store is ever touched.
	h := newUnitHandlers(&stubStore{}, &stubGames{}, &stubPrices{}, newStubCache())

	req := httptest.NewRequest(http.MethodPost, "/admin/products", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Authorization", "Bearer "+admin)
	rec := httptest.NewRecorder()
	NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil }).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUnitCreateCommunityProduct_Cover(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})

	var got store.Product
	st := &stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		got = p
		p.ID = uuid.NewString()
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		return p, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	// A non-https cover is 400 invalid_body; the store never runs.
	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", admin,
		map[string]any{"type": "game", "name": "Repro Cover", "cover_url": "http://img.example/x.jpg"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("http cover: %d, want 400", rec.Code)
	}

	// A valid https cover stores into the community facts block and
	// projects onto community.cover_url.
	rec = serveUnit(t, h, env, http.MethodPost, "/admin/products", admin, map[string]any{
		"type": "game", "name": "Repro Cover", "platform_name": "SNES",
		"cover_url": "https://img.example/rc.jpg",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	if got.Community == nil || got.Community.CoverURL != "https://img.example/rc.jpg" {
		t.Fatalf("stored cover wrong: %+v", got.Community)
	}
	var out api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Community == nil || out.Community.CoverUrl == nil || *out.Community.CoverUrl != "https://img.example/rc.jpg" {
		t.Fatalf("response cover missing: %+v", out.Community)
	}
}

// TestUnitCreateCommunityProduct_CoverOnlyMint pins the `|| hasCover`
// gate term in CreateCommunityProduct: a mint with ONLY a cover (no
// platform_name, no first_release_date) must still build the
// community facts block. TestUnitCreateCommunityProduct_Cover above
// always pairs its cover with platform_name, so that gate term stays
// unpinned without this case.
func TestUnitCreateCommunityProduct_CoverOnlyMint(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})

	var got store.Product
	st := &stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		got = p
		p.ID = uuid.NewString()
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		return p, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", admin, map[string]any{
		"type": "game", "name": "Cover Only", "cover_url": "https://img.example/co.jpg",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint: %d %s", rec.Code, rec.Body.String())
	}
	if got.Community == nil || got.Community.CoverURL != "https://img.example/co.jpg" {
		t.Fatalf("stored cover wrong: %+v", got.Community)
	}
	if got.Community.PlatformName != "" || !got.Community.FirstReleaseDate.IsZero() {
		t.Fatalf("cover-only mint must not fabricate other community fields: %+v", got.Community)
	}
	var out api.Product
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Community == nil || out.Community.CoverUrl == nil || *out.Community.CoverUrl != "https://img.example/co.jpg" {
		t.Fatalf("response cover missing: %+v", out.Community)
	}
}

// TestUnitCreateCommunityProduct_CoverLengthBoundary pins
// validCoverURL's 512-char boundary through the handler (no direct-unit
// sibling pattern exists for this file's helpers - every case here
// goes through serveUnit).
func TestUnitCreateCommunityProduct_CoverLengthBoundary(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})

	st := &stubStore{createProduct: func(_ context.Context, p store.Product) (store.Product, error) {
		p.ID = uuid.NewString()
		now := time.Now().UTC()
		p.CreatedAt, p.UpdatedAt = now, now
		return p, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	const prefix = "https://img.example/"
	url512 := prefix + strings.Repeat("a", 512-len(prefix))
	if len(url512) != 512 {
		t.Fatalf("fixture length = %d, want 512", len(url512))
	}
	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products", admin,
		map[string]any{"type": "game", "name": "Boundary 512", "cover_url": url512})
	if rec.Code != http.StatusCreated {
		t.Fatalf("512-char cover: %d %s, want 201", rec.Code, rec.Body.String())
	}

	url513 := url512 + "a"
	rec = serveUnit(t, h, env, http.MethodPost, "/admin/products", admin,
		map[string]any{"type": "game", "name": "Boundary 513", "cover_url": url513})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("513-char cover: %d, want 400", rec.Code)
	}
}

func TestUnitSearch_InterleavesCommunityAndKeepsCacheProviderOnly(t *testing.T) {
	env := newAuthEnv(t)
	user := env.token(t, uuid.NewString(), []string{"user"})
	now := time.Now().UTC()
	comm := store.Product{
		ID: "c0ffee00-0000-4000-8000-000000000001", Type: "game", Name: "Chrono Trigger",
		Origin: "community", Community: &store.CommunityMeta{PlatformName: "SNES", CoverURL: "https://img.example/ct.jpg", Region: "pal"},
		CreatedAt: now, UpdatedAt: now,
	}
	st := &stubStore{searchCommunityProducts: func(_ context.Context, types []string, _ string, limit int) ([]store.Product, error) {
		if limit != 10 || len(types) != 1 || types[0] != "game" {
			t.Fatalf("lane call wrong: types=%v limit=%d", types, limit)
		}
		return []store.Product{comm}, nil
	}}
	// Provider returns a weaker-scoring name so the exact community hit
	// leads the merged order.
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 4242, Name: "Chrono Cross"}}, nil
	}}
	c := newStubCache()
	h := newUnitHandlers(st, games, &stubPrices{}, c)

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono%20trigger", user, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	var out api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("merged results = %d, want 2", len(out.Results))
	}
	// Exact community hit outscores the provider near-match and leads.
	first := out.Results[0]
	if first.Origin == nil || string(*first.Origin) != "community" || first.Name != "Chrono Trigger" {
		t.Fatalf("community item should lead: %+v", first)
	}
	if first.ProductId == nil || first.ItemType == nil || string(*first.ItemType) != "game" ||
		first.PlatformName == nil || *first.PlatformName != "SNES" ||
		first.CoverUrl == nil || *first.CoverUrl != "https://img.example/ct.jpg" {
		t.Fatalf("community pick fields missing: %+v", first)
	}
	// The community region is entry vocabulary meant to seed the
	// wizard's region field straight from the search result, so a
	// community hit must carry it as its own top-level fact.
	if first.Region == nil || *first.Region != "pal" {
		t.Fatalf("community row must carry region pal, got %+v", first.Region)
	}
	if out.Results[1].Origin != nil {
		t.Fatalf("provider item must have no origin: %+v", out.Results[1])
	}
	// A provider game's region data lives in its localization bundles,
	// not a single top-level field - carrying one here would claim a
	// provider result has exactly one region when it may ship several.
	if out.Results[1].Region != nil {
		t.Fatalf("provider row must not carry region, got %+v", out.Results[1].Region)
	}

	// The cached body is provider-only: no community item leaked into cache.
	cached := c.search["game:"+normQuery("chrono trigger")]
	if cached == nil {
		// Key scheme is opaque; assert over whatever the stub stored.
		for _, v := range c.search {
			cached = v
		}
	}
	if cached == nil {
		t.Fatal("provider body was not cached")
	}
	if strings.Contains(string(cached), "community") {
		t.Fatalf("cached body must stay provider-only: %s", cached)
	}
}

// TestUnitSearch_InterleaveTieBreakProviderWinsEqualScore pins the
// provider-wins-on-a-tie branch in interleaveCommunityResults: a
// provider result and a community product with the identical name
// score exactly equal against the query, and the sort must still place
// the provider row first. TestUnitSearch_InterleavesCommunityAndKeepsCacheProviderOnly
// above uses a weaker-scoring provider name, so the tie branch itself
// stays unpinned without this case.
func TestUnitSearch_InterleaveTieBreakProviderWinsEqualScore(t *testing.T) {
	env := newAuthEnv(t)
	user := env.token(t, uuid.NewString(), []string{"user"})
	now := time.Now().UTC()
	comm := store.Product{
		ID: "c0ffee00-0000-4000-8000-000000000002", Type: "game", Name: "Chrono Trigger",
		Origin: "community", CreatedAt: now, UpdatedAt: now,
	}
	st := &stubStore{searchCommunityProducts: func(_ context.Context, types []string, _ string, limit int) ([]store.Product, error) {
		return []store.Product{comm}, nil
	}}
	// The provider result carries the exact same name as the community
	// product, so match.Score computes the identical value for both
	// against the same query - a genuine tie, not a near-miss.
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 4242, Name: "Chrono Trigger"}}, nil
	}}
	h := newUnitHandlers(st, games, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono%20trigger", user, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	var out api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 {
		t.Fatalf("merged results = %d, want 2", len(out.Results))
	}
	if out.Results[0].Origin != nil {
		t.Fatalf("provider row must lead an equal-score tie: %+v", out.Results[0])
	}
	if out.Results[1].Origin == nil || string(*out.Results[1].Origin) != "community" {
		t.Fatalf("community row must trail an equal-score tie: %+v", out.Results[1])
	}
}

// TestUnitSearch_CommunityLaneErrorFailsOpen pins the community lane's
// fail-open contract: SearchCatalog is otherwise entirely fail-open
// (cache errors go through h.failOpen, a down provider degrades to the
// local catalog), so a community store fault must not throw away the
// provider results already resolved in out - it degrades to a
// provider-only 200, not the lone hard 500 in this handler.
func TestUnitSearch_CommunityLaneErrorFailsOpen(t *testing.T) {
	env := newAuthEnv(t)
	user := env.token(t, uuid.NewString(), []string{"user"})
	st := &stubStore{searchCommunityProducts: func(context.Context, []string, string, int) ([]store.Product, error) {
		return nil, errors.New("community store down")
	}}
	games := &stubGames{searchGames: func(context.Context, string, int) ([]igdb.Game, error) {
		return []igdb.Game{{ID: 4242, Name: "Chrono Trigger"}}, nil
	}}
	h := newUnitHandlers(st, games, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=game&q=chrono%20trigger", user, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("community lane error: %d %s, want 200 (fail open)", rec.Code, rec.Body.String())
	}
	var out api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].Name != "Chrono Trigger" {
		t.Fatalf("provider results must survive a community fault: %+v", out.Results)
	}
	if out.Results[0].Origin != nil {
		t.Fatalf("no community row when the lane errors: %+v", out.Results[0])
	}
}

// TestUnitSearch_CommunityLaneHardwareTypes pins
// interleaveCommunityResults's hardware branch: a hardware-kind search
// must scope the community lane query to exactly [console accessory],
// never the bare "hardware" wire kind (which is not a stored product
// type).
func TestUnitSearch_CommunityLaneHardwareTypes(t *testing.T) {
	env := newAuthEnv(t)
	user := env.token(t, uuid.NewString(), []string{"user"})
	var gotTypes []string
	st := &stubStore{searchCommunityProducts: func(_ context.Context, types []string, _ string, limit int) ([]store.Product, error) {
		gotTypes = types
		if limit != 10 {
			t.Fatalf("lane limit = %d, want 10", limit)
		}
		return nil, nil
	}}
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, &stubGames{}, prices, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=hardware&q=repro", user, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	if !slices.Equal(gotTypes, []string{"console", "accessory"}) {
		t.Fatalf("hardware lane types = %v, want [console accessory]", gotTypes)
	}
}

func TestUnitSearch_NoLaneForPCListings(t *testing.T) {
	env := newAuthEnv(t)
	user := env.token(t, uuid.NewString(), []string{"user"})
	// searchCommunityProducts left nil: reaching the lane would panic.
	st := &stubStore{}
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, &stubGames{}, prices, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=pc_listing&q=repro", user, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	var out api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, r := range out.Results {
		if r.Origin != nil {
			t.Fatalf("pc_listing search must carry no community items: %+v", r)
		}
	}
}

// TestUnitSearch_NoLaneForPCListingsOnCacheHit is the cache-hit twin of
// TestUnitSearch_NoLaneForPCListings: the pc_listing default branch in
// interleaveCommunityResults is reached from two call sites in
// SearchCatalog (the early return on a cache hit, and the fall-through
// after a fresh provider search); this pins the first one specifically,
// priming the cache with a real request and then proving the second
// request never touches the provider (would degrade if it did) or the
// community store (left with no searchCommunityProducts stub; a call
// would panic).
func TestUnitSearch_NoLaneForPCListingsOnCacheHit(t *testing.T) {
	env := newAuthEnv(t)
	user := env.token(t, uuid.NewString(), []string{"user"})
	c := newStubCache()
	st := &stubStore{}
	prices := &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) { return nil, nil }}
	h := newUnitHandlers(st, &stubGames{}, prices, c)

	rec := serveUnit(t, h, env, http.MethodGet, "/search?type=pc_listing&q=repro", user, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search (miss): %d %s", rec.Code, rec.Body.String())
	}
	if c.puts == 0 {
		t.Fatal("first request must have primed the cache")
	}

	// Poison the provider: a cache-hit search must never reach it.
	h.prices = &stubPrices{search: func(context.Context, string) ([]pricecharting.Product, error) {
		return nil, errors.New("provider must not be called on a cache hit")
	}}
	rec = serveUnit(t, h, env, http.MethodGet, "/search?type=pc_listing&q=repro", user, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search (hit): %d %s", rec.Code, rec.Body.String())
	}
	var out api.SearchResults
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Degraded {
		t.Fatal("cache-hit search must not degrade; the provider must not have been called")
	}
	for _, r := range out.Results {
		if r.Origin != nil {
			t.Fatalf("pc_listing cache-hit must carry no community items: %+v", r)
		}
	}
}

func TestUnitAdminMapping_CommunityRefused(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	comm := store.Product{ID: uuid.NewString(), Type: "console", Name: "Handheld Mod", Origin: "community"}
	// prices stub left empty: any provider call would panic, proving
	// the guard fires before the mapping machinery.
	st := &stubStore{getProduct: func(context.Context, string) (store.Product, error) { return comm, nil }}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPut, "/admin/products/"+comm.ID+"/pricecharting", admin,
		map[string]any{"pc_product_id": 5005})
	if rec.Code != http.StatusConflict {
		t.Fatalf("community mapping: %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "product_not_provider") {
		t.Fatalf("code missing: %s", rec.Body.String())
	}
}

// TestCreateCommunityProduct_RegionInBlock pins that a community
// mint's region lands in the community facts block, not the
// top-level region field: community products carry no provider
// hardware identity, so region is a curated entry-vocabulary fact
// that belongs alongside the other community facts, not the field
// hardware identity uses (migration 000006 moved existing data to
// match; TestMigration_CommunityRegionMovesIntoBlock in the store
// package proves that rename against pre-migration documents).
func TestCreateCommunityProduct_RegionInBlock(t *testing.T) {
	s := newStack(t)

	resp := s.do(http.MethodPost, "/admin/products", s.adminToken(), map[string]any{
		"type": "game", "name": "PachiPals", "region": "ntsc_j",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("mint: %d", resp.StatusCode)
	}
	out := decodeBody[api.Product](t, resp)
	if out.Region != nil {
		t.Fatalf("top-level region must stay absent on community mints, got %q", *out.Region)
	}
	if out.Community == nil || out.Community.Region == nil || *out.Community.Region != "ntsc_j" {
		t.Fatalf("response community.region wrong: %+v", out.Community)
	}

	got, err := s.store.GetProduct(context.Background(), out.Id.String())
	if err != nil {
		t.Fatal(err)
	}
	if got.Region != "" {
		t.Fatalf("stored top-level region must stay empty, got %q", got.Region)
	}
	if got.Community == nil || got.Community.Region != "ntsc_j" {
		t.Fatalf("stored community.region wrong: %+v", got.Community)
	}
}

func TestPromoteCommunityProduct(t *testing.T) {
	s := newStack(t)
	mint := func(name string) string {
		resp := s.do(http.MethodPost, "/admin/products", s.adminToken(), map[string]any{
			"type": "game", "name": name, "platform_name": "SNES",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("mint: %d", resp.StatusCode)
		}
		return decodeBody[api.Product](t, resp).Id.String()
	}
	first := mint("Chrono Trigger Repro A")
	second := mint("Chrono Trigger Repro B")

	resp := s.do(http.MethodPost, "/admin/products/"+first+"/promote", s.userToken(),
		map[string]any{"igdb_game_id": 1011, "platform_igdb_id": 19})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin promote: %d", resp.StatusCode)
	}

	resp = s.do(http.MethodPost, "/admin/products/"+first+"/promote", s.adminToken(),
		map[string]any{"platform_igdb_id": 19})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing igdb_game_id: %d", resp.StatusCode)
	}

	resp = s.do(http.MethodPost, "/admin/products/"+first+"/promote", s.adminToken(),
		map[string]any{"igdb_game_id": 1011, "platform_igdb_id": 19})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("promote: %d", resp.StatusCode)
	}
	promoted := decodeBody[api.Product](t, resp)
	if promoted.Origin != nil {
		t.Fatal("promoted product must not emit origin")
	}
	if promoted.Igdb == nil || promoted.Igdb.GameId != 1011 {
		t.Fatalf("igdb block missing: %+v", promoted.Igdb)
	}
	if promoted.Community == nil || promoted.Community.PlatformName == nil {
		t.Fatal("community facts must be retained as gap-fill")
	}

	resp = s.do(http.MethodPost, "/admin/products/"+first+"/promote", s.adminToken(),
		map[string]any{"igdb_game_id": 1011, "platform_igdb_id": 19})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("re-promote: %d, want 409 product_not_community", resp.StatusCode)
	}

	// Twin: the (game, platform, no-listing) slot is taken; the index
	// adjudicates and the detail names the holder.
	resp = s.do(http.MethodPost, "/admin/products/"+second+"/promote", s.adminToken(),
		map[string]any{"igdb_game_id": 1011, "platform_igdb_id": 19})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("twin promote: %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "identity_taken") || !strings.Contains(string(body), first) {
		t.Fatalf("twin detail must carry the code and name the holder: %s", body)
	}
}

// TestUnitPromoteHardware_HappyPathBuildsAnchorAndSnapshots pins the
// console/accessory promote branch: PromoteProduct must receive the pc
// anchor built from the fetched listing (verified=true, unlike a plain
// resolve's machine-made anchor) and carry no igdb/platform anchors
// (those are the game branch's job), and a successful anchor promote
// must append exactly one snapshot.
func TestUnitPromoteHardware_HappyPathBuildsAnchorAndSnapshots(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	comm := store.Product{ID: uuid.NewString(), Type: "console", Name: "Handheld Mod", Origin: "community"}
	loose := int64(4200)

	var (
		gotID           string
		gotIGDB         *store.IGDBMeta
		gotPlatform     *store.Platform
		gotPC           *store.PCMeta
		snapshotCalled  bool
		gotSnap         store.Snapshot
		getProductCalls int
	)
	st := &stubStore{
		getProduct: func(context.Context, string) (store.Product, error) {
			getProductCalls++
			if getProductCalls == 1 {
				return comm, nil
			}
			// The post-promote reload: the handler serves whatever
			// GetProduct now answers (the flipped, anchored state).
			return store.Product{
				ID: comm.ID, Type: "console", Name: comm.Name, Origin: "provider",
				PriceCharting: &store.PCMeta{PCProductID: 5005, PCName: "Handheld Mod", ConsoleName: "Nintendo 64", Current: store.PriceQuote{LooseCents: &loose}},
			}, nil
		},
		promoteProduct: func(_ context.Context, id string, igdbMeta *store.IGDBMeta, platform *store.Platform, pc *store.PCMeta) error {
			gotID, gotIGDB, gotPlatform, gotPC = id, igdbMeta, platform, pc
			return nil
		},
		appendSnapshot: func(_ context.Context, s store.Snapshot) error {
			snapshotCalled = true
			gotSnap = s
			return nil
		},
	}
	prices := &stubPrices{product: func(_ context.Context, id int64) (pricecharting.Product, error) {
		if id != 5005 {
			t.Fatalf("wrong pc product id requested: %d", id)
		}
		return pricecharting.Product{ID: 5005, Name: "Handheld Mod", ConsoleName: "Nintendo 64", LoosePriceCents: &loose}, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, prices, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products/"+comm.ID+"/promote", admin,
		map[string]any{"pc_product_id": 5005})
	if rec.Code != http.StatusOK {
		t.Fatalf("hardware promote: %d %s", rec.Code, rec.Body.String())
	}
	if gotID != comm.ID {
		t.Fatalf("promote id = %q, want %q", gotID, comm.ID)
	}
	if gotIGDB != nil || gotPlatform != nil {
		t.Fatalf("hardware promote must carry no igdb/platform anchors: igdb=%+v platform=%+v", gotIGDB, gotPlatform)
	}
	if gotPC == nil || gotPC.PCProductID != 5005 || gotPC.ConsoleName != "Nintendo 64" {
		t.Fatalf("pc anchor wrong: %+v", gotPC)
	}
	if gotPC.MatchConfidence != 1.0 || !gotPC.Verified {
		t.Fatalf("a promote-built pc anchor must be exact and admin-verified: %+v", gotPC)
	}
	if !snapshotCalled {
		t.Fatal("AppendSnapshot must fire for the pc anchor")
	}
	if gotSnap.ProductID != comm.ID || gotSnap.LooseCents == nil || *gotSnap.LooseCents != loose {
		t.Fatalf("snapshot wrong: %+v", gotSnap)
	}
}

// TestUnitPromoteGame_UnknownGameIs404 pins the game branch's
// gamePayloadFor unknown-game outcome (raw miss, then a provider fetch
// that returns zero games) routing through resolveError's *resolveErr
// path onto 404 unknown_game - the same taxonomy the resolve flow's
// TestResolve_ErrorTaxonomy pins for /products/resolve, but promote has
// its own call site (h.resolveError(w, r, gerr) in PromoteProduct) that
// needs its own direct proof.
func TestUnitPromoteGame_UnknownGameIs404(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	comm := store.Product{ID: uuid.NewString(), Type: "game", Name: "Chrono Trigger Repro", Origin: "community"}
	st := &stubStore{
		getProduct: func(context.Context, string) (store.Product, error) { return comm, nil },
		rawByIDs:   func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
	}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) { return nil, nil }}
	h := newUnitHandlers(st, games, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products/"+comm.ID+"/promote", admin,
		map[string]any{"igdb_game_id": 999999, "platform_igdb_id": 19})

	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown_game") {
		t.Fatalf("unknown game: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitPromoteGame_ProviderOutageIs502 pins the game branch's
// upstream_unavailable outcome: no raw on file and the provider fetch
// itself failing (not just returning zero games) must answer 502, not
// the 500 a raw-read/DB fault earns (TestUnitPromoteGame_RawFaultIsInternal
// pins that adjacent branch).
func TestUnitPromoteGame_ProviderOutageIs502(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	comm := store.Product{ID: uuid.NewString(), Type: "game", Name: "Chrono Trigger Repro", Origin: "community"}
	st := &stubStore{
		getProduct: func(context.Context, string) (store.Product, error) { return comm, nil },
		rawByIDs:   func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
	}
	games := &stubGames{gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) {
		return nil, errors.New("igdb down")
	}}
	h := newUnitHandlers(st, games, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products/"+comm.ID+"/promote", admin,
		map[string]any{"igdb_game_id": 1011, "platform_igdb_id": 19})

	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "upstream_unavailable") {
		t.Fatalf("provider outage: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitPromoteHardware_UnknownPCProductIs404 pins the hardware
// branch's own unknown-listing outcome: the pc anchor fetch answering
// pricecharting.ErrNotFound maps to 404 unknown_pc_product (the same
// code TestResolve_ErrorTaxonomy pins for /products/resolve's hardware
// path; promote's console/accessory case shares the same
// h.prices.Product call shape but needs its own direct proof since it
// is a distinct call site in PromoteProduct).
func TestUnitPromoteHardware_UnknownPCProductIs404(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	comm := store.Product{ID: uuid.NewString(), Type: "console", Name: "Handheld Mod", Origin: "community"}
	st := &stubStore{getProduct: func(context.Context, string) (store.Product, error) { return comm, nil }}
	prices := &stubPrices{product: func(context.Context, int64) (pricecharting.Product, error) {
		return pricecharting.Product{}, pricecharting.ErrNotFound
	}}
	h := newUnitHandlers(st, &stubGames{}, prices, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products/"+comm.ID+"/promote", admin,
		map[string]any{"pc_product_id": 999999})

	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "unknown_pc_product") {
		t.Fatalf("unknown pc product: %d %s", rec.Code, rec.Body.String())
	}
}

// TestUnitPromoteGame_RawFaultIsInternal pins the promote game branch's
// non-resolveErr fallthrough onto 500 internal, matching the resolve
// idiom: a raw-read Mongo fault is an internal DB error, never the 502
// upstream_unavailable a provider outage earns.
func TestUnitPromoteGame_RawFaultIsInternal(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	comm := store.Product{ID: uuid.NewString(), Type: "game", Name: "Chrono Trigger Repro", Origin: "community"}
	// rawByIDs faults with a plain error (a Mongo fault), which
	// gamePayloadFor wraps as a non-resolveErr; games/prices stay empty
	// because reaching a provider here would be the wrong path.
	st := &stubStore{
		getProduct: func(context.Context, string) (store.Product, error) { return comm, nil },
		rawByIDs:   func(context.Context, []int64) ([]store.RawGame, error) { return nil, errors.New("mongo unreachable") },
	}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodPost, "/admin/products/"+comm.ID+"/promote", admin,
		map[string]any{"igdb_game_id": 1011, "platform_igdb_id": 19})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("raw-read fault: %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal") {
		t.Fatalf("want code internal: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "upstream_unavailable") {
		t.Fatalf("DB fault must not be classified as an upstream 502: %s", rec.Body.String())
	}
}

func TestUnitCandidateSweep_FlagsAndSkipsDismissed(t *testing.T) {
	comm := store.Product{
		ID: uuid.NewString(), Type: "game", Name: "Chrono Trigger", Origin: "community",
		DismissedCandidates: []store.CandidateRef{{Provider: "igdb", ProviderID: 1099}},
	}
	var gotCands []store.PromoteCandidate
	st := &stubStore{
		listCommunityProducts: func(context.Context) ([]store.Product, error) {
			return []store.Product{comm}, nil
		},
		replacePromoteCandidates: func(_ context.Context, id string, cands []store.PromoteCandidate) error {
			if id != comm.ID {
				t.Fatalf("wrong product: %s", id)
			}
			gotCands = cands
			return nil
		},
	}
	games := &stubGames{searchGames: func(_ context.Context, q string, _ int) ([]igdb.Game, error) {
		return []igdb.Game{
			{ID: 1011, Name: "Chrono Trigger"},     // fresh, above threshold
			{ID: 1099, Name: "Chrono Trigger"},     // dismissed pair: silent
			{ID: 1002, Name: "A Link to the Past"}, // below threshold
		}, nil
	}}
	h := newUnitHandlers(st, games, &stubPrices{}, newStubCache())

	h.runCandidateSweep(context.Background())

	if len(gotCands) != 1 || gotCands[0].ProviderID != 1011 || gotCands[0].Provider != "igdb" {
		t.Fatalf("sweep candidates = %+v, want only fresh 1011", gotCands)
	}
	if gotCands[0].Score < match.Threshold {
		t.Fatalf("stored score below threshold: %v", gotCands[0].Score)
	}
}

// TestUnitCandidateSweep_HardwareFlagsSkipsDismissedAndSortsBestFirst
// pins the sweep's console/accessory branch (candidates sourced from
// h.prices.Search and scored via match.Score against the community
// product's name, mirroring the game branch's igdb search but over
// PriceCharting listings), plus the write-side ordering: with multiple
// surviving candidates, ReplacePromoteCandidates must receive them
// sorted best-first. That sort is runCandidateSweep's own
// sort.SliceStable call before the store write - store.ReplacePromoteCandidates
// itself is a plain $set with no ordering logic (see store.go), so this
// is the correct seam for the ordering assertion, not a store-level
// test.
func TestUnitCandidateSweep_HardwareFlagsSkipsDismissedAndSortsBestFirst(t *testing.T) {
	comm := store.Product{
		ID: uuid.NewString(), Type: "console", Name: "Nintendo 64 Console", Origin: "community",
		DismissedCandidates: []store.CandidateRef{{Provider: "pricecharting", ProviderID: 5099}},
	}
	var gotCands []store.PromoteCandidate
	st := &stubStore{
		listCommunityProducts: func(context.Context) ([]store.Product, error) {
			return []store.Product{comm}, nil
		},
		replacePromoteCandidates: func(_ context.Context, id string, cands []store.PromoteCandidate) error {
			if id != comm.ID {
				t.Fatalf("wrong product: %s", id)
			}
			gotCands = cands
			return nil
		},
	}
	prices := &stubPrices{search: func(_ context.Context, q string) ([]pricecharting.Product, error) {
		return []pricecharting.Product{
			// Deliberately scrambled (weakest qualifying match first):
			// the sweep must re-sort; the store must not have to.
			{ID: 5010, Name: "Nintendo 64 Console Bundle"}, // above threshold, weaker (dice 6/7)
			{ID: 5099, Name: "Nintendo 64 Console"},        // dismissed pair: silent regardless of score
			{ID: 5005, Name: "Nintendo 64 Console"},        // exact name: strongest (dice 1.0)
			{ID: 5001, Name: "PlayStation"},                // below threshold: excluded
		}, nil
	}}
	h := newUnitHandlers(st, &stubGames{}, prices, newStubCache())

	h.runCandidateSweep(context.Background())

	if len(gotCands) != 2 {
		t.Fatalf("sweep candidates = %+v, want 2 (dismissed pair and below-threshold hit excluded)", gotCands)
	}
	if gotCands[0].ProviderID != 5005 || gotCands[1].ProviderID != 5010 {
		t.Fatalf("candidates must arrive sorted best-first: %+v", gotCands)
	}
	if gotCands[0].Score <= gotCands[1].Score {
		t.Fatalf("scores must strictly descend: %v then %v", gotCands[0].Score, gotCands[1].Score)
	}
	for _, c := range gotCands {
		if c.Provider != "pricecharting" {
			t.Fatalf("hardware branch must flag pricecharting candidates: %+v", c)
		}
	}
}

func TestUnitPromoteCandidates_ListAndDismiss(t *testing.T) {
	env := newAuthEnv(t)
	admin := env.token(t, uuid.NewString(), []string{"user", "admin"})
	user := env.token(t, uuid.NewString(), []string{"user"})
	now := time.Now().UTC()
	flagged := store.Product{
		ID: uuid.NewString(), Type: "console", Name: "Handheld Mod", Origin: "community",
		PromoteCandidates: []store.PromoteCandidate{{Provider: "pricecharting", ProviderID: 5005, Name: "Super Mario 64", Score: 0.9, FoundAt: now}},
		CreatedAt:         now, UpdatedAt: now,
	}
	var dismissed struct {
		id, provider string
		providerID   int64
	}
	st := &stubStore{
		listPromoteCandidateProducts: func(_ context.Context, limit, offset int, productID string) ([]store.Product, int64, error) {
			return []store.Product{flagged}, 1, nil
		},
		dismissPromoteCandidate: func(_ context.Context, id, provider string, providerID int64) error {
			dismissed.id, dismissed.provider, dismissed.providerID = id, provider, providerID
			return nil
		},
	}
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, newStubCache())

	rec := serveUnit(t, h, env, http.MethodGet, "/admin/products/promote-candidates", user, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin list: %d", rec.Code)
	}
	rec = serveUnit(t, h, env, http.MethodGet, "/admin/products/promote-candidates", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var page api.PromoteCandidatesPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.TotalCount != 1 || len(page.Products) != 1 || len(page.Products[0].Candidates) != 1 {
		t.Fatalf("page shape: %+v", page)
	}

	rec = serveUnit(t, h, env, http.MethodPost, "/admin/products/"+flagged.ID+"/promote-candidates/dismiss", admin,
		map[string]any{"provider": "pricecharting", "provider_id": 5005})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body.String())
	}
	if dismissed.id != flagged.ID || dismissed.provider != "pricecharting" || dismissed.providerID != 5005 {
		t.Fatalf("dismiss args: %+v", dismissed)
	}
}

func TestUnitListPlatforms_JoinsAliasesSortsAndCaches(t *testing.T) {
	env := newAuthEnv(t)
	user := env.token(t, uuid.NewString(), []string{"user"})
	var storeCalls int
	st := &stubStore{
		// Fresh stamp keeps ensurePlatforms on the cached-catalog path
		// (never touches the IGDB stub).
		platformsFetchedAt: func(context.Context) (time.Time, error) { return time.Now().UTC(), nil },
		listPlatforms: func(context.Context) ([]store.CatalogPlatform, error) {
			storeCalls++
			return []store.CatalogPlatform{
				{ID: 19, Name: "Super Nintendo Entertainment System"},
				{ID: 18, Name: "Nintendo Entertainment System"},
				// An alias-less platform: PlatformAliases returns nil, which
				// must still serialize as [] - the contract types aliases a
				// required string[], and the picker filters over it with no
				// null guard.
				{ID: 23, Name: "Dreamcast"},
			}, nil
		},
	}
	c := newStubCache()
	h := newUnitHandlers(st, &stubGames{}, &stubPrices{}, c)

	rec := serveUnit(t, h, env, http.MethodGet, "/platforms", user, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("platforms: %d %s", rec.Code, rec.Body.String())
	}
	var out api.PlatformCatalog
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// The alias-less row must serialize aliases as [], never null: the
	// contract types it a required string[] and the picker filters over
	// it with no null guard.
	if body := rec.Body.String(); !strings.Contains(body, `"aliases":[]`) || strings.Contains(body, `"aliases":null`) {
		t.Fatalf("alias-less platform must serialize aliases:[] not null: %s", body)
	}
	// Sorted by name: Dreamcast precedes the Nintendo rows.
	if len(out.Platforms) != 3 || out.Platforms[0].Name != "Dreamcast" {
		t.Fatalf("sort by name failed: %+v", out.Platforms)
	}
	var snes *api.CatalogPlatform
	for i := range out.Platforms {
		if out.Platforms[i].IgdbId == 19 {
			snes = &out.Platforms[i]
		}
	}
	if snes == nil {
		t.Fatalf("snes row missing: %+v", out.Platforms)
	}
	if !slices.Contains(snes.Aliases, "snes") {
		t.Fatalf("snes aliases missing 'snes': %v", snes.Aliases)
	}

	// The second call is served from Valkey: the store is not read again.
	before := storeCalls
	rec = serveUnit(t, h, env, http.MethodGet, "/platforms", user, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cached platforms: %d", rec.Code)
	}
	if storeCalls != before {
		t.Fatalf("second call hit the store, want cache")
	}
}

// ---- InternalNormalizeCommunityRegions ----

// TestUnitInternalNormalizeCommunityRegions_PromotesFoldMatchSkipsUnknown
// pins the fold+synonym promotion (enrichment's twin of collection's
// normalize-regions lever, scoped to the community products this
// service owns): a reviewed synonym promotes through the twin tables,
// a graduated region promotes through its identity fold, an
// unreviewed string is left untouched, and the response counts all
// three.
func TestUnitInternalNormalizeCommunityRegions_PromotesFoldMatchSkipsUnknown(t *testing.T) {
	env := newAuthEnv(t)
	promoted := "p-japan"
	promotedKR := "p-korea"
	untouched := "p-taiwan"
	var wrote []struct{ id, region string }
	st := &stubStore{
		listCommunityRegionDocs: func(context.Context) ([]store.CommunityRegionRef, error) {
			return []store.CommunityRegionRef{
				{ID: promoted, Region: "Japan"},
				{ID: promotedKR, Region: "Korea"},
				{ID: untouched, Region: "Taiwan"},
			}, nil
		},
		setCommunityRegion: func(_ context.Context, id, region string) error {
			wrote = append(wrote, struct{ id, region string }{id, region})
			return nil
		},
	}
	h := newUnitHandlers(st, nil, nil, newStubCache())
	admin := env.token(t, "admin1", []string{"admin"})

	rec := serveUnit(t, h, env, http.MethodPost, "/internal/normalize-community-regions", admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var counts map[string]int
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if counts["scanned"] != 3 || counts["normalized"] != 2 || counts["skipped"] != 1 {
		t.Fatalf("counts = %+v, want scanned 3 normalized 2 skipped 1", counts)
	}
	if len(wrote) != 2 || wrote[0].id != promoted || wrote[0].region != "ntsc_j" {
		t.Fatalf("wrote = %+v, want the first write promoting %q to ntsc_j", wrote, promoted)
	}
	if wrote[1].id != promotedKR || wrote[1].region != "korea" {
		t.Fatalf("wrote = %+v, want the second write promoting %q to korea", wrote, promotedKR)
	}
}

// TestUnitInternalNormalizeCommunityRegions_Guards mirrors collection's
// normalize-regions guard tests: a service token (the nightly job's
// own credential) passes, a plain user token is forbidden.
func TestUnitInternalNormalizeCommunityRegions_Guards(t *testing.T) {
	env := newAuthEnv(t)
	st := &stubStore{listCommunityRegionDocs: func(context.Context) ([]store.CommunityRegionRef, error) { return nil, nil }}
	h := newUnitHandlers(st, nil, nil, newStubCache())

	svc := env.serviceToken(t, "svc:normalize-community-regions")
	rec := serveUnit(t, h, env, http.MethodPost, "/internal/normalize-community-regions", svc, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("service token: status %d, want 200: %s", rec.Code, rec.Body.String())
	}

	user := env.token(t, "u1", []string{"user"})
	rec = serveUnit(t, h, env, http.MethodPost, "/internal/normalize-community-regions", user, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("plain user: status %d, want 403: %s", rec.Code, rec.Body.String())
	}
}
