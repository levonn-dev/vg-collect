// Tests for the catalog refresh runner and its admin/CronJob
// endpoints, and community region normalization.

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
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/match"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/pricecharting"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

// ---------------------------------------------------------------
// Refresh runner + admin endpoints
// ---------------------------------------------------------------

// doInternal drives the CronJob path: a Bearer service token instead
// of a user's own.
func (s *stack) doInternal(bearer string) *http.Response {
	s.t.Helper()
	req := reqtest.NewJSONRequest(s.t, http.MethodPost, s.srv.URL+"/internal/refresh", bearer, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	return resp
}

// serveInternal is the unit-layer equivalent of doInternal.
func serveInternal(t *testing.T, h *Handlers, env *authEnv, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := reqtest.NewJSONRequest(t, http.MethodPost, "/internal/refresh", bearer, nil)
	rec := httptest.NewRecorder()
	router, err := NewRouter(h, env.validator(), slog.New(slog.DiscardHandler), func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
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
	reqtest.WaitFor(t, 10*time.Second, func() bool {
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
	reqtest.WaitFor(t, 10*time.Second, func() bool {
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
	reqtest.WaitFor(t, 10*time.Second, func() bool { return !s.h.refreshing.Load() })
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
	reqtest.WaitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
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
	reqtest.WaitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })

	// The guard resets: a third trigger is accepted again.
	st.listPriced = func(context.Context) ([]store.Product, error) { return nil, nil }
	rec = serveInternal(t, h, env, tok)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post-refresh trigger: %d", rec.Code)
	}
	reqtest.WaitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
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
	reqtest.WaitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
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
	reqtest.WaitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })

	// The guard reset after the panic: a second trigger is accepted
	// again, not 409 (a leaked guard would answer 409 forever).
	st.listPriced = func(context.Context) ([]store.Product, error) { return nil, nil }
	rec = serveInternal(t, h, env, tok)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("post-panic trigger: %d", rec.Code)
	}
	reqtest.WaitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
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
	reqtest.WaitFor(t, 5*time.Second, func() bool { return !h.refreshing.Load() })
	if !called {
		t.Fatal("startRefresh must run the reprojection pass")
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

// ---- InternalNormalizeCommunityRegions ----

// TestUnitInternalNormalizeCommunityRegions_PromotesFoldMatchSkipsUnknown
// pins the fold+synonym promotion (enrichment's twin of collection's
// normalize-regions lever, scoped to the community products this
// service owns): a reviewed synonym promotes through regionkit's synonym table,
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
