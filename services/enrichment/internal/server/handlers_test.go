package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/levonn-dev/vgkeep/libs/go/mongokit"
	"github.com/levonn-dev/vgkeep/libs/go/mongotest"
	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/libs/go/valkeykit"
	"github.com/levonn-dev/vgkeep/libs/go/valkeytest"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/cache"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/fx"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
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
	searchByName                 func(ctx context.Context, types []string, q string, limit int) ([]store.Product, error)
	searchCommunityProducts      func(ctx context.Context, types []string, q string, limit int) ([]store.Product, error)
	listCommunityRegionDocs      func(ctx context.Context, known []string) ([]store.CommunityRegionRef, error)
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

func (s *stubStore) SearchByName(ctx context.Context, types []string, q string, limit int) ([]store.Product, error) {
	if s.searchByName == nil {
		panic("unexpected SearchByName")
	}
	return s.searchByName(ctx, types, q, limit)
}

func (s *stubStore) SearchCommunityProducts(ctx context.Context, types []string, q string, limit int) ([]store.Product, error) {
	if s.searchCommunityProducts == nil {
		panic("unexpected SearchCommunityProducts")
	}
	return s.searchCommunityProducts(ctx, types, q, limit)
}

func (s *stubStore) ListCommunityRegionDocs(ctx context.Context, known []string) ([]store.CommunityRegionRef, error) {
	if s.listCommunityRegionDocs == nil {
		panic("unexpected ListCommunityRegionDocs")
	}
	return s.listCommunityRegionDocs(ctx, known)
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
	req := reqtest.NewJSONRequest(t, method, path, token, body)
	rec := httptest.NewRecorder()
	router, err := NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
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

// newStack shares one MongoDB container across this whole package via
// mongotest.URL (the Valkey half below uses the same shared-container
// pattern via valkeytest.URL) and still owns the drop + re-migrate
// reset, so every test opens on a fresh, fully migrated database
// regardless of what an earlier test left behind.
func newStack(t *testing.T) *stack {
	t.Helper()
	ctx := context.Background()

	url := mongotest.URL(t)
	mclient, err := mongokit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mclient.Disconnect(context.Background()) })
	db := mongotest.DBName(t)
	if err := mclient.Database(db).Drop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := mongokit.Migrate(ctx, url, db, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mdb := mclient.Database(db)
	st := store.New(mdb)

	rdb, err := valkeykit.Connect(ctx, valkeytest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	// Reset: flush whatever the previous test cached so each test
	// starts on an empty cache. FlushDB, not FlushAll: the URL is
	// scoped to this binary's own database on the shared server, and
	// FlushAll would wipe every other binary's data too.
	if err := rdb.FlushDB(ctx).Err(); err != nil {
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
	router, err := NewRouter(h, env.validator(), slog.New(slog.DiscardHandler),
		func(c context.Context) error { return mongokit.Health(c, mclient) })
	if err != nil {
		t.Fatal(err)
	}
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
	req := reqtest.NewJSONRequest(s.t, method, s.srv.URL+path, token, body)
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp
}

func decodeBody[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	return reqtest.DecodeJSON[T](t, resp)
}

// resolveGame is the shared shortcut used across the handler tests to
// drive a game through the resolve handler and decode the result.
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
