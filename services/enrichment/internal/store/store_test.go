package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	mongodriver "github.com/golang-migrate/migrate/v4/database/mongodb"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/levonn-dev/vgkeep/libs/go/mongokit"
	"github.com/levonn-dev/vgkeep/libs/go/mongotest"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
	"github.com/levonn-dev/vgkeep/services/enrichment/migrations"
)

// newTestStore returns a Store plus the raw db handle, on a freshly
// migrated database (mongotest.FreshDB resets between tests; skips under -short).
func newTestStore(t *testing.T) (*store.Store, *mongo.Database) {
	t.Helper()
	mdb := mongotest.FreshDB(t, migrations.FS, ".")
	return store.New(mdb), mdb
}

func gameProduct(gameID, platformID int64, name, platformName, variant string) store.Product {
	return store.Product{
		Type:     "game",
		Name:     name,
		Platform: &store.Platform{IGDBID: platformID, Name: platformName},
		IGDB: &store.IGDBMeta{
			GameID: gameID, Name: name,
			Genres: []store.Genre{{ID: 12, Name: "Role-playing (RPG)"}},
			Themes: []string{"Fantasy"}, Franchises: []string{}, SimilarGames: []int64{},
			Companies: []store.Company{{Name: "Square", Developer: true, Publisher: true}},
			FetchedAt: time.Now().UTC().Truncate(time.Millisecond),
		},
		Variant: variant,
	}
}

func TestProduct_FindCreateGet(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	key := store.ProductKey{Type: "game", IGDBGameID: 1011, PlatformIGDBID: 19}

	if _, err := s.FindProduct(ctx, key); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound before create, got %v", err)
	}

	created, err := s.CreateProduct(ctx, gameProduct(1011, 19, "Chrono Trigger", "Super Nintendo Entertainment System", ""))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() {
		t.Fatal("create must mint id and timestamps")
	}

	found, err := s.FindProduct(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != created.ID || found.IGDB == nil || found.IGDB.GameID != 1011 {
		t.Fatalf("found wrong product: %+v", found)
	}

	got, err := s.GetProduct(ctx, created.ID)
	if err != nil || got.Name != "Chrono Trigger" {
		t.Fatalf("get: %+v, %v", got, err)
	}
	if _, err := s.GetProduct(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// Game identity is listing-keyed: members converge per (game,
// platform, listing); the unmatched member is the family's singleton.
func TestProduct_GameMembersAreListingKeyed(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	matched := func(pcID int64) store.Product {
		p := gameProduct(1005, 4, "Super Mario 64", "Nintendo 64", "")
		p.PriceCharting = &store.PCMeta{
			PCProductID: pcID, PCName: "Super Mario 64", ConsoleName: "Nintendo 64",
			MatchConfidence: 1.0, Verified: false,
			AsOf: time.Now().UTC().Truncate(time.Millisecond),
		}
		return p
	}

	// One unmatched member per family, variant text ignored.
	u1, err := s.CreateProduct(ctx, gameProduct(1005, 4, "Super Mario 64", "Nintendo 64", ""))
	if err != nil {
		t.Fatal(err)
	}
	u2, err := s.CreateProduct(ctx, gameProduct(1005, 4, "Super Mario 64", "Nintendo 64", "not for resale"))
	if err != nil {
		t.Fatal(err)
	}
	if u1.ID != u2.ID {
		t.Fatalf("unmatched members must converge: %s vs %s", u1.ID, u2.ID)
	}

	// Same listing converges; a different listing is a different member.
	m1, err := s.CreateProduct(ctx, matched(5005))
	if err != nil {
		t.Fatal(err)
	}
	m1again, err := s.CreateProduct(ctx, matched(5005))
	if err != nil {
		t.Fatal(err)
	}
	if m1.ID != m1again.ID {
		t.Fatalf("same-listing members must converge: %s vs %s", m1.ID, m1again.ID)
	}
	m2, err := s.CreateProduct(ctx, matched(5099))
	if err != nil {
		t.Fatal(err)
	}
	if m2.ID == m1.ID || m2.ID == u1.ID {
		t.Fatal("a different listing must be a distinct member")
	}

	// FindProduct addresses each member through the key.
	got, err := s.FindProduct(ctx, store.ProductKey{Type: "game", IGDBGameID: 1005, PlatformIGDBID: 4})
	if err != nil || got.ID != u1.ID {
		t.Fatalf("null key must find the unmatched member: %+v, %v", got, err)
	}
	got, err = s.FindProduct(ctx, store.ProductKey{Type: "game", IGDBGameID: 1005, PlatformIGDBID: 4, PCProductID: 5099})
	if err != nil || got.ID != m2.ID {
		t.Fatalf("listing key must find its member: %+v, %v", got, err)
	}
}

// Concurrent resolves of one identity must converge on a single
// product (unique index + duplicate-key re-find in CreateProduct).
func TestProduct_ConcurrentCreate_SingleWinner(t *testing.T) {
	s, mdb := newTestStore(t)
	ctx := context.Background()

	const n = 10
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := s.CreateProduct(ctx, gameProduct(1013, 7, "Final Fantasy VII", "PlayStation", ""))
			ids[i], errs[i] = p.ID, err
		}(i)
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("racer %d errored: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("racers diverged: %s vs %s", ids[i], ids[0])
		}
	}
	count, err := mdb.Collection("products").CountDocuments(ctx, map[string]any{"type": "game"})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want exactly 1 product, got %d", count)
	}
}

func TestProduct_SubdocUpdates(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	p, err := s.CreateProduct(ctx, gameProduct(1011, 19, "Chrono Trigger", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}

	// Unmatched at first: SetCurrentPrices must refuse (nothing to
	// update), and ListPriced must skip it.
	if err := s.SetCurrentPrices(ctx, p.ID, store.PriceQuote{}, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("prices on unmatched product: want ErrNotFound, got %v", err)
	}
	priced, err := s.ListPriced(ctx)
	if err != nil || len(priced) != 0 {
		t.Fatalf("unmatched product must not be walked: %d, %v", len(priced), err)
	}

	loose := int64(4200)
	pc := &store.PCMeta{
		PCProductID: 5011, PCName: "Chrono Trigger", ConsoleName: "Super Nintendo",
		MatchConfidence: 1.0, Verified: false,
		Current: store.PriceQuote{LooseCents: &loose},
		AsOf:    time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := s.SetPriceCharting(ctx, p.ID, pc); err != nil {
		t.Fatal(err)
	}
	priced, err = s.ListPriced(ctx)
	if err != nil || len(priced) != 1 {
		t.Fatalf("matched product must be walked: %d, %v", len(priced), err)
	}

	newLoose := int64(4300)
	if err := s.SetCurrentPrices(ctx, p.ID, store.PriceQuote{LooseCents: &newLoose}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetProduct(ctx, p.ID)
	if got.PriceCharting == nil || *got.PriceCharting.Current.LooseCents != 4300 {
		t.Fatalf("current prices not updated: %+v", got.PriceCharting)
	}

	// Fresher IGDB projection replaces the old one.
	fresh := *p.IGDB
	fresh.Name = "Chrono Trigger (refetched)"
	fresh.FetchedAt = time.Now().UTC().Truncate(time.Millisecond)
	if err := s.SetIGDB(ctx, p.ID, fresh); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetProduct(ctx, p.ID)
	if got.IGDB.Name != "Chrono Trigger (refetched)" {
		t.Fatal("igdb projection not replaced")
	}

	// Clearing the mapping makes the product unmatched again.
	if err := s.SetPriceCharting(ctx, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetProduct(ctx, p.ID)
	if got.PriceCharting != nil {
		t.Fatal("mapping not cleared")
	}
	if err := s.SetIGDB(ctx, "00000000-0000-0000-0000-000000000000", fresh); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound for unknown id, got %v", err)
	}
}

// Mapping writes are identity moves: a taken slot surfaces
// ErrIdentityTaken; clears set MatchHold, any set lifts it.
func TestProduct_MappingWritesHoldAndIdentityTaken(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	meta := func(pcID int64) *store.PCMeta {
		return &store.PCMeta{
			PCProductID: pcID, PCName: "Super Mario 64", ConsoleName: "Nintendo 64",
			MatchConfidence: 0.9, Verified: false,
			AsOf: time.Now().UTC().Truncate(time.Millisecond),
		}
	}

	unmatched, err := s.CreateProduct(ctx, gameProduct(1005, 4, "Super Mario 64", "Nintendo 64", ""))
	if err != nil {
		t.Fatal(err)
	}
	m := gameProduct(1005, 4, "Super Mario 64", "Nintendo 64", "")
	m.PriceCharting = meta(5005)
	matched, err := s.CreateProduct(ctx, m)
	if err != nil {
		t.Fatal(err)
	}

	// Clearing the matched member collides with the existing unmatched
	// member (two null keys in one family).
	if err := s.SetPriceCharting(ctx, matched.ID, nil); !errors.Is(err, store.ErrIdentityTaken) {
		t.Fatalf("clear onto an occupied null slot: want ErrIdentityTaken, got %v", err)
	}

	// Mapping the unmatched member frees the family's null slot, so
	// the matched member's clear below now lands instead of colliding.
	if err := s.SetPriceCharting(ctx, unmatched.ID, meta(5099)); err != nil {
		t.Fatal(err)
	}

	// The admin clear is now free (the family's null slot emptied
	// above) and sets the hold; a following set lifts it.
	if err := s.SetPriceCharting(ctx, matched.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProduct(ctx, matched.ID)
	if err != nil || got.PriceCharting != nil || !got.MatchHold {
		t.Fatalf("clear must unmap and hold: %+v, %v", got, err)
	}
	if err := s.SetPriceCharting(ctx, matched.ID, meta(5005)); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetProduct(ctx, matched.ID)
	if err != nil || got.PriceCharting == nil || got.MatchHold {
		t.Fatalf("set must map and lift the hold: %+v, %v", got, err)
	}
}

// ListIGDBProducts returns every igdb-bearing product, including ones
// with a release table already (unfiltered so a future projection
// change still revisits them), excludes hardware, sorted by _id.
func TestProduct_ListIGDBProducts(t *testing.T) {
	s, mdb := newTestStore(t)
	ctx := context.Background()

	withDates := func(id string, gameID int64) {
		t.Helper()
		now := time.Now().UTC().Truncate(time.Millisecond)
		_, err := mdb.Collection("products").InsertOne(ctx, bson.D{
			{Key: "_id", Value: id}, {Key: "type", Value: "game"},
			{Key: "name", Value: "Game " + id},
			{Key: "platform", Value: bson.D{{Key: "igdb_id", Value: int64(19)}, {Key: "name", Value: "SNES"}}},
			{Key: "region", Value: ""}, {Key: "edition", Value: ""}, {Key: "variant", Value: ""},
			{Key: "igdb", Value: bson.D{
				{Key: "game_id", Value: gameID}, {Key: "name", Value: "Game " + id},
				{Key: "fetched_at", Value: now},
				{Key: "release_dates", Value: bson.A{
					bson.D{{Key: "region", Value: "japan"}, {Key: "date", Value: now}},
				}},
			}},
			{Key: "created_at", Value: now}, {Key: "updated_at", Value: now},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// Two products already carrying a release table, one pre-feature
	// (release_dates absent), one hardware product with no igdb subdoc.
	withDates("id-1-withdates", 3001)
	seedPreFeatureProduct(t, mdb, "id-2-prefeature", 3002, 4, time.Now().UTC().Add(-time.Hour))
	withDates("id-3-withdates", 3003)
	if _, err := s.CreateProduct(ctx, store.Product{Type: "console", Name: "Nintendo 64 Console"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListIGDBProducts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := []string{"id-1-withdates", "id-2-prefeature", "id-3-withdates"}
	if len(got) != len(wantIDs) {
		t.Fatalf("want %d igdb-bearing products (hardware excluded), got %d: %+v", len(wantIDs), len(got), got)
	}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Fatalf("position %d: want _id %s, got %s", i, id, got[i].ID)
		}
	}
}

// seedPreFeatureProduct inserts a game product whose igdb subdoc
// predates the release-dates feature (release_dates key absent, not
// empty), bypassing SetIGDB via a direct collection insert.
func seedPreFeatureProduct(t *testing.T, mdb *mongo.Database, id string, gameID, platformID int64, fetchedAt time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err := mdb.Collection("products").InsertOne(context.Background(), bson.D{
		{Key: "_id", Value: id}, {Key: "type", Value: "game"},
		{Key: "name", Value: "Pre-Feature " + id},
		{Key: "platform", Value: bson.D{{Key: "igdb_id", Value: platformID}, {Key: "name", Value: "Seed Platform"}}},
		{Key: "region", Value: ""}, {Key: "edition", Value: ""}, {Key: "variant", Value: ""},
		{Key: "igdb", Value: bson.D{
			{Key: "game_id", Value: gameID},
			{Key: "name", Value: "Pre-Feature " + id},
			{Key: "fetched_at", Value: fetchedAt},
			// release_dates deliberately absent: the pre-feature sentinel.
		}},
		{Key: "created_at", Value: now}, {Key: "updated_at", Value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProduct_ByIDsAndSearch(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a, _ := s.CreateProduct(ctx, gameProduct(1011, 19, "Chrono Trigger", "SNES", ""))
	b, _ := s.CreateProduct(ctx, gameProduct(1012, 7, "Chrono Cross", "PlayStation", ""))

	got, err := s.ProductsByIDs(ctx, []string{a.ID, "00000000-0000-0000-0000-000000000000", b.ID})
	if err != nil || len(got) != 2 {
		t.Fatalf("by ids: %d, %v", len(got), err)
	}

	// A community product with a matching name must not leak into the
	// degraded provider fallback (it has no provider identity to serve).
	if _, err := s.CreateProduct(ctx, store.Product{Type: "game", Name: "Chrono Repro", Origin: "community"}); err != nil {
		t.Fatal(err)
	}
	hits, err := s.SearchByName(ctx, []string{"game"}, "chrono", 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("search: %d, %v", len(hits), err)
	}
	if hits[0].Name != "Chrono Cross" {
		t.Fatalf("want name-sorted results, got %s first", hits[0].Name)
	}
	one, _ := s.SearchByName(ctx, []string{"game"}, "CHRONO", 1)
	if len(one) != 1 {
		t.Fatal("limit or case-insensitivity broken")
	}
}

// Confirms SearchByName also matches via igdb.localizations native
// name or transliteration when the canonical Name misses.
func TestSearchByName_MatchesLocalizationNameAndTranslit(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	p := gameProduct(1001, 4, "The Legend of Zelda: Ocarina of Time", "Nintendo 64", "")
	p.IGDB.Localizations = []store.MetaLocalization{
		{Region: "ja-JP", Name: "ゼルダの伝説 時のオカリナ", Translit: "Zelda no Densetsu: Toki no Ocarina"},
	}
	if _, err := s.CreateProduct(ctx, p); err != nil {
		t.Fatal(err)
	}

	byName, err := s.SearchByName(ctx, []string{"game"}, "ゼルダの伝説", 10)
	if err != nil || len(byName) != 1 || byName[0].Name != "The Legend of Zelda: Ocarina of Time" {
		t.Fatalf("localization name match: %d, %v", len(byName), err)
	}

	byTranslit, err := s.SearchByName(ctx, []string{"game"}, "TOKI NO OCARINA", 10)
	if err != nil || len(byTranslit) != 1 || byTranslit[0].Name != "The Legend of Zelda: Ocarina of Time" {
		t.Fatalf("translit match (case-insensitive): %d, %v", len(byTranslit), err)
	}

	none, err := s.SearchByName(ctx, []string{"game"}, "no such text", 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("want no match, got %d, %v", len(none), err)
	}
}

func TestSnapshotsSinceWindowsAndOrders(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
	cents := func(v int64) *int64 { return &v }

	// Product A: one point outside the window, two inside (inserted
	// newest-first, to prove the read sorts); product B has one point.
	for _, snap := range []store.Snapshot{
		{ProductID: "prod-a", CapturedAt: base.AddDate(0, 0, -40), LooseCents: cents(1000)},
		{ProductID: "prod-a", CapturedAt: base.AddDate(0, 0, -1), LooseCents: cents(1300)},
		{ProductID: "prod-a", CapturedAt: base.AddDate(0, 0, -10), LooseCents: cents(1200)},
		{ProductID: "prod-b", CapturedAt: base.AddDate(0, 0, -5), CIBCents: cents(4200)},
	} {
		if err := s.AppendSnapshot(ctx, snap); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.SnapshotsSince(ctx, []string{"prod-a", "prod-b", "prod-missing"}, base.AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want series for 2 products, got %d", len(got))
	}
	a := got["prod-a"]
	if len(a) != 2 {
		t.Fatalf("prod-a: want 2 in-window points, got %d", len(a))
	}
	if !a[0].CapturedAt.Before(a[1].CapturedAt) {
		t.Fatalf("prod-a points not oldest-first: %v then %v", a[0].CapturedAt, a[1].CapturedAt)
	}
	if *a[0].LooseCents != 1200 || *a[1].LooseCents != 1300 {
		t.Fatalf("prod-a values wrong: %d, %d", *a[0].LooseCents, *a[1].LooseCents)
	}
	if len(got["prod-b"]) != 1 || *got["prod-b"][0].CIBCents != 4200 {
		t.Fatalf("prod-b series wrong: %+v", got["prod-b"])
	}
	if _, ok := got["prod-missing"]; ok {
		t.Fatal("id with no snapshots must be absent, not empty")
	}

	empty, err := s.SnapshotsSince(ctx, nil, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty id set: want empty map, got %+v", empty)
	}
}

func TestNewIGDBMeta_Projection(t *testing.T) {
	g := igdb.Game{
		ID: 1011, Name: "Chrono Trigger",
		Cover:             &igdb.Cover{ImageID: "co_fx1011"},
		Genres:            []igdb.Named{{ID: 12, Name: "Role-playing (RPG)"}},
		Themes:            []igdb.Named{{ID: 17, Name: "Fantasy"}, {ID: 18, Name: "Science fiction"}},
		Franchises:        []igdb.Named{{ID: 813, Name: "Chrono"}},
		SimilarGames:      []int64{1012, 1014},
		InvolvedCompanies: []igdb.InvolvedCompany{{Company: igdb.Named{ID: 26, Name: "Square"}, Developer: true, Publisher: true}},
		FirstReleaseDate:  788918400,
		Platforms:         []igdb.Named{{ID: 19, Name: "Super Nintendo Entertainment System"}},
	}
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	m := store.NewIGDBMeta(g, 19, at)
	wantDate := time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC)
	if m.GameID != 1011 || m.Name != "Chrono Trigger" || !m.FirstReleaseDate.Equal(wantDate) {
		t.Fatalf("core fields: %+v", m)
	}
	if m.CoverURL != "https://images.igdb.com/igdb/image/upload/t_cover_big/co_fx1011.jpg" {
		t.Fatalf("cover url: %s", m.CoverURL)
	}
	if len(m.Genres) != 1 || m.Genres[0].ID != 12 {
		t.Fatalf("genres: %+v", m.Genres)
	}
	if len(m.Themes) != 2 || m.Themes[0] != "Fantasy" {
		t.Fatalf("themes: %+v", m.Themes)
	}
	if len(m.Franchises) != 1 || m.Franchises[0] != "Chrono" {
		t.Fatalf("franchises: %+v", m.Franchises)
	}
	if len(m.SimilarGames) != 2 || len(m.Companies) != 1 || !m.Companies[0].Developer {
		t.Fatalf("edges/companies: %+v", m)
	}
	if !m.FetchedAt.Equal(at) {
		t.Fatalf("fetched_at: %v", m.FetchedAt)
	}
}

func TestNewIGDBMeta_PlatformScopedReleaseDates(t *testing.T) {
	g := igdb.Game{
		ID: 1011, Name: "Chrono Trigger", FirstReleaseDate: 794880000,
		ReleaseDates: []igdb.ReleaseDate{
			{Date: 794880000, Platform: 19, Region: 5},  // SNES japan
			{Date: 809049600, Platform: 19, Region: 2},  // SNES north_america
			{Date: 809049600, Platform: 19, Region: 2},  // duplicate region: keep earliest
			{Date: 1200000000, Platform: 20, Region: 2}, // other platform: dropped
			{Platform: 19, Region: 1},                   // dateless: dropped
			{Date: 794880000, Platform: 19, Region: 99}, // unknown region enum: dropped
		},
	}
	m := store.NewIGDBMeta(g, 19, time.Now())
	want := []store.MetaReleaseDate{
		{Region: "japan", Date: time.Unix(794880000, 0).UTC().Truncate(24 * time.Hour)},
		{Region: "north_america", Date: time.Unix(809049600, 0).UTC().Truncate(24 * time.Hour)},
	}
	if len(m.ReleaseDates) != 2 || m.ReleaseDates[0] != want[0] || m.ReleaseDates[1] != want[1] {
		t.Fatalf("release dates: %+v", m.ReleaseDates)
	}
	if !m.FirstReleaseDate.Equal(want[0].Date) {
		t.Fatalf("scalar must be the platform's earliest: %v", m.FirstReleaseDate)
	}
}

func TestNewIGDBMeta_NoPlatformRowsFallsBackToGameLevel(t *testing.T) {
	g := igdb.Game{
		ID: 1, Name: "X", FirstReleaseDate: 794880000,
		ReleaseDates: []igdb.ReleaseDate{{Date: 900000000, Platform: 7, Region: 2}},
	}
	m := store.NewIGDBMeta(g, 19, time.Now())
	if len(m.ReleaseDates) != 0 || m.ReleaseDates == nil {
		t.Fatalf("want empty non-nil list, got %+v", m.ReleaseDates)
	}
	if !m.FirstReleaseDate.Equal(g.ReleaseDate()) {
		t.Fatalf("scalar must fall back to game level: %v", m.FirstReleaseDate)
	}
}

// JP-twin fold: Super Famicom (58) rows appear on a SNES (19) product
// and vice versa (same for NES 18 / Family Computer 99); unrelated
// platforms and platform-0 rows never fold; scalar is the folded earliest.
func TestNewIGDBMeta_FoldsTwinPlatformRows(t *testing.T) {
	japan := time.Unix(794880000, 0).UTC().Truncate(24 * time.Hour)
	na := time.Unix(809049600, 0).UTC().Truncate(24 * time.Hour)
	twin := igdb.Game{
		ID: 1, Name: "Chrono Trigger", FirstReleaseDate: 794880000,
		ReleaseDates: []igdb.ReleaseDate{
			{Date: 794880000, Platform: 58, Region: 5}, // Super Famicom japan (earliest)
			{Date: 809049600, Platform: 19, Region: 2}, // SNES north_america
			{Date: 900000000, Platform: 7, Region: 1},  // PlayStation: unrelated, never folds
			{Date: 700000000, Platform: 0, Region: 8},  // platform 0: dropped though earliest
		},
	}
	wantFold := []store.MetaReleaseDate{{Region: "japan", Date: japan}, {Region: "north_america", Date: na}}

	// Either side of the SNES/Super Famicom pair sees the same folded set.
	for _, pid := range []int64{19, 58} {
		m := store.NewIGDBMeta(twin, pid, time.Now())
		if len(m.ReleaseDates) != 2 || m.ReleaseDates[0] != wantFold[0] || m.ReleaseDates[1] != wantFold[1] {
			t.Fatalf("pid %d fold: %+v", pid, m.ReleaseDates)
		}
		if !m.FirstReleaseDate.Equal(japan) {
			t.Fatalf("pid %d scalar must be the folded earliest (japan): %v", pid, m.FirstReleaseDate)
		}
	}

	// The NES/Family Computer pair folds both ways too.
	fc := time.Unix(500000000, 0).UTC().Truncate(24 * time.Hour)
	nesNA := time.Unix(520000000, 0).UTC().Truncate(24 * time.Hour)
	nesTwin := igdb.Game{
		ID: 2, Name: "Twin NES", FirstReleaseDate: 500000000,
		ReleaseDates: []igdb.ReleaseDate{
			{Date: 500000000, Platform: 99, Region: 5}, // Family Computer japan
			{Date: 520000000, Platform: 18, Region: 2}, // NES north_america
		},
	}
	wantNES := []store.MetaReleaseDate{{Region: "japan", Date: fc}, {Region: "north_america", Date: nesNA}}
	for _, pid := range []int64{18, 99} {
		m := store.NewIGDBMeta(nesTwin, pid, time.Now())
		if len(m.ReleaseDates) != 2 || m.ReleaseDates[0] != wantNES[0] || m.ReleaseDates[1] != wantNES[1] {
			t.Fatalf("pid %d NES fold: %+v", pid, m.ReleaseDates)
		}
	}

	// A platform-0 product (no platform ref) folds nothing and keeps no
	// rows: the platform-0 guard drops the row on its own terms.
	zero := store.NewIGDBMeta(twin, 0, time.Now())
	if len(zero.ReleaseDates) != 0 || zero.ReleaseDates == nil {
		t.Fatalf("platform 0 must scope to an empty, non-nil release table: %#v", zero.ReleaseDates)
	}
}

// Localization bundles land in the projection 1:1; a game with none
// projects an empty, non-nil slice (matches ReleaseDates/SimilarGames).
func TestNewIGDBMeta_Localizations(t *testing.T) {
	g := igdb.Game{
		ID: 1011, Name: "Chrono Trigger",
		GameLocalizations: []igdb.GameLocalization{
			{Name: "クロノ・トリガー", Region: igdb.LocalizationRegion{Identifier: "ja-JP"}, Cover: &igdb.Cover{ImageID: "co_fx1011jp"}},
		},
		AlternativeNames: []igdb.AlternativeName{
			{Name: "Kurono Torigaa", Comment: "Japanese title - romanization"},
		},
	}
	m := store.NewIGDBMeta(g, 19, time.Now())
	want := []store.MetaLocalization{{
		Region: "ja-JP", Name: "クロノ・トリガー", Translit: "Kurono Torigaa",
		CoverURL: "https://images.igdb.com/igdb/image/upload/t_cover_big/co_fx1011jp.jpg",
	}}
	if len(m.Localizations) != 1 || m.Localizations[0] != want[0] {
		t.Fatalf("localizations: %+v", m.Localizations)
	}

	empty := store.NewIGDBMeta(igdb.Game{ID: 1, Name: "X"}, 19, time.Now())
	if len(empty.Localizations) != 0 || empty.Localizations == nil {
		t.Fatalf("want empty non-nil localizations, got %#v", empty.Localizations)
	}
}

// SameProjection ignores FetchedAt; differing rows compare unequal;
// nil release table is NOT equal to an empty one (pre-feature marker).
func TestIGDBMeta_SameProjection(t *testing.T) {
	base := store.IGDBMeta{
		GameID: 1011, Name: "Chrono Trigger",
		Genres:        []store.Genre{{ID: 12, Name: "Role-playing (RPG)"}},
		Themes:        []string{"Fantasy"},
		Franchises:    []string{"Chrono"},
		SimilarGames:  []int64{1012},
		Companies:     []store.Company{{Name: "Square", Developer: true, Publisher: true}},
		ReleaseDates:  []store.MetaReleaseDate{{Region: "japan", Date: time.Unix(794880000, 0).UTC()}},
		Localizations: []store.MetaLocalization{{Region: "ja-JP", Name: "クロノ・トリガー"}},
		FetchedAt:     time.Unix(1000, 0).UTC(),
	}
	sameButNewer := base
	sameButNewer.FetchedAt = time.Unix(9_999_999, 0).UTC() // provider stamp differs only
	if !base.SameProjection(sameButNewer) {
		t.Fatal("projections identical apart from FetchedAt must compare equal")
	}

	differing := base
	differing.ReleaseDates = []store.MetaReleaseDate{{Region: "europe", Date: time.Unix(1, 0).UTC()}}
	if base.SameProjection(differing) {
		t.Fatal("differing release rows must compare unequal")
	}

	nilTable := base
	nilTable.ReleaseDates = nil
	emptyTable := base
	emptyTable.ReleaseDates = []store.MetaReleaseDate{}
	if nilTable.SameProjection(emptyTable) {
		t.Fatal("nil vs empty release table must compare unequal (pre-feature vs fetched-none)")
	}

	nilLoc := base
	nilLoc.Localizations = nil
	emptyLoc := base
	emptyLoc.Localizations = []store.MetaLocalization{}
	if nilLoc.SameProjection(emptyLoc) {
		t.Fatal("nil vs empty localizations must compare unequal (pre-feature vs fetched-none)")
	}
}

// Deletes only game products whose region/edition/variant forked
// identity; clean games and hardware survive; rebuilt index enforces the singleton.
func TestMigration_ListingKeyedIdentityDeletesTupleResidue(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	client, err := mongokit.Connect(ctx, mongotest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	// Needs a virgin database, not just a virgin server: drop it so the
	// pinned partial migrate below starts clean.
	db := mongotest.DBName(t)
	if err := client.Database(db).Drop(ctx); err != nil {
		t.Fatal(err)
	}

	driver, err := mongodriver.WithInstance(client, &mongodriver.Config{
		DatabaseName: db,
		Locking:      mongodriver.Locking{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, db, driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(3); err != nil {
		t.Fatalf("migrate to pre-listing-keyed state: %v", err)
	}

	col := client.Database(db).Collection("products")
	now := time.Now().UTC().Truncate(time.Millisecond)
	seed := func(id, typ, region, edition, variant string, gameID, platformID int64) {
		t.Helper()
		_, err := col.InsertOne(ctx, bson.D{
			{Key: "_id", Value: id}, {Key: "type", Value: typ},
			{Key: "name", Value: "Seed " + id},
			{Key: "platform", Value: bson.D{{Key: "igdb_id", Value: platformID}, {Key: "name", Value: "Seed"}}},
			{Key: "region", Value: region}, {Key: "edition", Value: edition}, {Key: "variant", Value: variant},
			{Key: "igdb", Value: bson.D{{Key: "game_id", Value: gameID}, {Key: "name", Value: "Seed " + id}}},
			{Key: "created_at", Value: now}, {Key: "updated_at", Value: now},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	seed("keeper-game", "game", "", "", "", 1011, 19)
	seed("residue-variant", "game", "", "", "not for resale", 1011, 19)
	seed("residue-region", "game", "pal", "", "", 1005, 4)
	seed("keeper-hardware", "console", "pal", "", "", 0, 19)

	// Pinned to version 4, not Up(): Up() would also run later
	// migrations and change what the duplicate-key assertion below exercises.
	if err := m.Migrate(4); err != nil {
		t.Fatalf("migrate to listing-keyed state: %v", err)
	}

	for id, want := range map[string]int64{
		"keeper-game": 1, "keeper-hardware": 1,
		"residue-variant": 0, "residue-region": 0,
	} {
		n, err := col.CountDocuments(ctx, bson.D{{Key: "_id", Value: id}})
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("%s: want %d docs, got %d", id, want, n)
		}
	}

	// The rebuilt index enforces the null singleton per family.
	_, err = col.InsertOne(ctx, bson.D{
		{Key: "_id", Value: "second-unmatched"}, {Key: "type", Value: "game"},
		{Key: "name", Value: "Seed dup"},
		{Key: "platform", Value: bson.D{{Key: "igdb_id", Value: int64(19)}, {Key: "name", Value: "Seed"}}},
		{Key: "region", Value: ""}, {Key: "edition", Value: ""}, {Key: "variant", Value: ""},
		{Key: "igdb", Value: bson.D{{Key: "game_id", Value: int64(1011)}, {Key: "name", Value: "Seed dup"}}},
		{Key: "created_at", Value: now}, {Key: "updated_at", Value: now},
	})
	if !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("want duplicate-key on the second unmatched member, got %v", err)
	}
}

// Pins migration 000006: a community product's top-level region
// moves to community.region, a sibling community fact survives
// untouched, and untouched paths (already-empty region, no region
// key, provider docs) come out matching the always-present shape rule.
func TestMigration_CommunityRegionMovesIntoBlock(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	client, err := mongokit.Connect(ctx, mongotest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	// Needs a virgin database, not just a virgin server: drop it so the
	// pinned partial migrate below starts clean.
	db := mongotest.DBName(t)
	if err := client.Database(db).Drop(ctx); err != nil {
		t.Fatal(err)
	}

	driver, err := mongodriver.WithInstance(client, &mongodriver.Config{
		DatabaseName: db,
		Locking:      mongodriver.Locking{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, db, driver)
	if err != nil {
		t.Fatal(err)
	}
	// Pinned to version 5: seeds the pre-000006 mint shape so migrating
	// to 6 exercises the rename against real old documents, not new ones.
	if err := m.Migrate(5); err != nil {
		t.Fatalf("migrate to pre-region-block state: %v", err)
	}

	col := client.Database(db).Collection("products")
	now := time.Now().UTC().Truncate(time.Millisecond)
	seed := func(doc bson.D) {
		t.Helper()
		base := bson.D{
			{Key: "type", Value: "game"},
			{Key: "created_at", Value: now}, {Key: "updated_at", Value: now},
		}
		if _, err := col.InsertOne(ctx, append(base, doc...)); err != nil {
			t.Fatal(err)
		}
	}
	seed(bson.D{
		{Key: "_id", Value: "with-region"}, {Key: "name", Value: "Seed With Region"},
		{Key: "origin", Value: "community"}, {Key: "region", Value: "ntsc_j"},
		{Key: "community", Value: bson.D{{Key: "platform_name", Value: "SNES"}}},
	})
	seed(bson.D{
		{Key: "_id", Value: "empty-region"}, {Key: "name", Value: "Seed Empty Region"},
		{Key: "origin", Value: "community"}, {Key: "region", Value: ""},
	})
	seed(bson.D{
		{Key: "_id", Value: "missing-region"}, {Key: "name", Value: "Seed Missing Region"},
		{Key: "origin", Value: "community"},
	})
	seed(bson.D{
		{Key: "_id", Value: "provider-hardware"}, {Key: "name", Value: "Seed Provider"},
		{Key: "origin", Value: "provider"}, {Key: "region", Value: "pal"},
	})

	if err := m.Migrate(6); err != nil {
		t.Fatalf("migrate to region-block state: %v", err)
	}

	mustFind := func(id string) bson.M {
		t.Helper()
		var d bson.M
		if err := col.FindOne(ctx, bson.D{{Key: "_id", Value: id}}).Decode(&d); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		return d
	}

	withRegion := mustFind("with-region")
	if withRegion["region"] != "" {
		t.Fatalf("with-region: top-level region must be renamed away, got %+v", withRegion["region"])
	}
	comm, _ := withRegion["community"].(bson.M)
	if comm == nil || comm["region"] != "ntsc_j" {
		t.Fatalf("with-region: community.region wrong: %+v", withRegion["community"])
	}
	if comm["platform_name"] != "SNES" {
		t.Fatalf("with-region: sibling community fact must survive untouched: %+v", comm)
	}

	emptyRegion := mustFind("empty-region")
	if emptyRegion["region"] != "" {
		t.Fatalf("empty-region: region must stay empty, got %+v", emptyRegion["region"])
	}
	if _, ok := emptyRegion["community"]; ok {
		t.Fatalf("empty-region: must not fabricate a community block, got %+v", emptyRegion["community"])
	}

	missingRegion := mustFind("missing-region")
	if missingRegion["region"] != "" {
		t.Fatalf("missing-region: shape-stability step must set region to \"\", got %+v", missingRegion["region"])
	}

	providerHardware := mustFind("provider-hardware")
	if providerHardware["region"] != "pal" {
		t.Fatalf("provider-hardware: provider region must be untouched, got %+v", providerHardware["region"])
	}
	if _, ok := providerHardware["community"]; ok {
		t.Fatalf("provider-hardware: must not gain a community block, got %+v", providerHardware["community"])
	}

	// down.json must undo the rename: confirms the reverse dotted-path
	// rename restores the top-level field, not split across both.
	if err := m.Migrate(5); err != nil {
		t.Fatalf("down to pre-region-block state: %v", err)
	}
	reverted := mustFind("with-region")
	if reverted["region"] != "ntsc_j" {
		t.Fatalf("down: top-level region must be restored, got %+v", reverted["region"])
	}
	revertedComm, _ := reverted["community"].(bson.M)
	if revertedComm == nil || revertedComm["platform_name"] != "SNES" {
		t.Fatalf("down: sibling community fact must survive, got %+v", reverted["community"])
	}
	if _, ok := revertedComm["region"]; ok {
		t.Fatalf("down: community.region must be removed after rename-back, got %+v", revertedComm)
	}
}

func TestListUnmatchedProducts(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// Creation order fixes updated_at order: unmatchedA is oldest.
	unmatchedA, err := s.CreateProduct(ctx, gameProduct(9001, 6, "Worklist Alpha", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}
	heldConsole, err := s.CreateProduct(ctx, store.Product{
		Type: "console", Name: "Worklist Console", Region: "pal", Variant: "wl",
	})
	if err != nil {
		t.Fatal(err)
	}
	matched, err := s.CreateProduct(ctx, gameProduct(9002, 6, "Worklist Matched", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPriceCharting(ctx, matched.ID, &store.PCMeta{
		PCProductID: 9101, PCName: "Worklist Matched", ConsoleName: "Super Nintendo",
		MatchConfidence: 1, AsOf: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	// A deliberate clear sets match_hold; held products MUST still
	// appear in the worklist (the hold pins the mapping, not visibility).
	if err := s.SetPriceCharting(ctx, heldConsole.ID, nil); err != nil {
		t.Fatal(err)
	}

	page, total, err := s.ListUnmatchedProducts(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (matched product excluded)", total)
	}
	if len(page) != 2 || page[0].ID != unmatchedA.ID || page[1].ID != heldConsole.ID {
		t.Fatalf("page order/content wrong: %+v", page)
	}
	if !page[1].MatchHold {
		t.Fatal("held console must carry MatchHold in the worklist")
	}

	// Offset paging slices the same deterministic order.
	page2, total2, err := s.ListUnmatchedProducts(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total2 != 2 || len(page2) != 1 || page2[0].ID != heldConsole.ID {
		t.Fatalf("offset page wrong: total=%d page=%+v", total2, page2)
	}
}

// TestListUnmatchedProducts_Indexed pins the migration-backed index
// so a growing worklist cannot regress into an unindexed sort.
func TestListUnmatchedProducts_Indexed(t *testing.T) {
	_, mdb := newTestStore(t)
	ctx := context.Background()

	cur, err := mdb.Collection("products").Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for cur.Next(ctx) {
		var idx bson.M
		if err := cur.Decode(&idx); err != nil {
			t.Fatal(err)
		}
		if idx["name"] == "products_unmatched_worklist" {
			found = true
		}
	}
	if !found {
		t.Fatal("products_unmatched_worklist index missing")
	}
}

func TestDeleteUnmatchedProduct(t *testing.T) {
	s, mdb := newTestStore(t)
	ctx := context.Background()

	orphan, err := s.CreateProduct(ctx, gameProduct(9401, 6, "Delete Me", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.AppendSnapshot(ctx, store.Snapshot{ProductID: orphan.ID, CapturedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	matched, err := s.CreateProduct(ctx, gameProduct(9402, 6, "Keep Me", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPriceCharting(ctx, matched.ID, &store.PCMeta{
		PCProductID: 9410, PCName: "Keep Me", ConsoleName: "Super Nintendo",
		MatchConfidence: 1, AsOf: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteUnmatchedProduct(ctx, orphan.ID)
	if err != nil || !deleted {
		t.Fatalf("orphan delete: deleted=%v err=%v", deleted, err)
	}
	if _, err := s.GetProduct(ctx, orphan.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("orphan must be gone, got %v", err)
	}
	n, err := mdb.Collection("price_snapshots").CountDocuments(ctx, map[string]any{"product_id": orphan.ID})
	if err != nil || n != 0 {
		t.Fatalf("orphan snapshots must be gone: n=%d err=%v", n, err)
	}

	// A matched product never deletes; a missing id reports the same.
	deleted, err = s.DeleteUnmatchedProduct(ctx, matched.ID)
	if err != nil || deleted {
		t.Fatalf("matched delete must refuse: deleted=%v err=%v", deleted, err)
	}
	if _, err := s.GetProduct(ctx, matched.ID); err != nil {
		t.Fatalf("matched product must survive: %v", err)
	}
	deleted, err = s.DeleteUnmatchedProduct(ctx, orphan.ID)
	if err != nil || deleted {
		t.Fatalf("missing delete must report false: deleted=%v err=%v", deleted, err)
	}
}

func TestCommunityOrigin_TwinsInsertAndExclusions(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// Two anchor-less community games with identical null identity
	// keys must both insert distinct (name is community identity).
	c1, err := s.CreateProduct(ctx, store.Product{Type: "game", Name: "Repro Alpha", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.CreateProduct(ctx, store.Product{Type: "game", Name: "Repro Beta", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	if c1.ID == c2.ID {
		t.Fatal("community twins must mint distinct products")
	}
	if c1.Origin != "community" {
		t.Fatalf("origin = %q, want community", c1.Origin)
	}

	// Same for anchor-less community consoles (empty region/edition/
	// variant would collide inside the hardware identity index).
	h1, err := s.CreateProduct(ctx, store.Product{Type: "console", Name: "Handheld Mod A", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := s.CreateProduct(ctx, store.Product{Type: "console", Name: "Handheld Mod B", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	if h1.ID == h2.ID {
		t.Fatal("community console twins must mint distinct products")
	}

	// Provider identity is untouched: the same game key still
	// find-or-mints onto one document.
	p1, err := s.CreateProduct(ctx, gameProduct(9301, 19, "Origin Provider", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}
	if p1.Origin != "provider" {
		t.Fatalf("provider origin = %q, want provider (stamped by CreateProduct)", p1.Origin)
	}
	p2, err := s.CreateProduct(ctx, gameProduct(9301, 19, "Origin Provider", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}
	if p1.ID != p2.ID {
		t.Fatal("provider identity must still find-or-mint one doc")
	}

	// Community products are unmatched forever by definition: the
	// admin worklist read excludes them.
	page, total, err := s.ListUnmatchedProducts(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range page {
		if p.Origin == "community" {
			t.Fatalf("admin worklist must exclude community products, got %q", p.Name)
		}
	}
	// p1 is unmatched (no listing) and provider: it must be counted.
	if total < 1 {
		t.Fatalf("total = %d, want >= 1 (provider unmatched still listed)", total)
	}

	// Community facts round-trip through the doc.
	rd := time.Date(1995, 10, 9, 0, 0, 0, 0, time.UTC)
	cf, err := s.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Repro Facts", Origin: "community",
		Community: &store.CommunityMeta{PlatformName: "SNES", FirstReleaseDate: rd},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProduct(ctx, cf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Community == nil || got.Community.PlatformName != "SNES" || !got.Community.FirstReleaseDate.Equal(rd) {
		t.Fatalf("community facts did not round-trip: %+v", got.Community)
	}
}

func TestListCommunityProductsPage(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	// Creation order fixes updated_at order; the sleep guards a
	// same-millisecond tie (updated_at truncates to ms, _id is a random UUID).
	communityA, err := s.CreateProduct(ctx, store.Product{Type: "game", Name: "Community Alpha", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	communityB, err := s.CreateProduct(ctx, store.Product{Type: "console", Name: "Community Beta", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	// A provider product must never surface here: promote (not this
	// listing) is how a community product becomes provider-origin.
	if _, err := s.CreateProduct(ctx, gameProduct(9501, 6, "Provider Excluded", "SNES", "")); err != nil {
		t.Fatal(err)
	}

	page, total, err := s.ListCommunityProductsPage(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2 (provider product excluded)", total)
	}
	if len(page) != 2 || page[0].ID != communityA.ID || page[1].ID != communityB.ID {
		t.Fatalf("page order/content wrong: %+v", page)
	}
	for _, p := range page {
		if p.Origin != "community" {
			t.Fatalf("admin community listing must only carry community origin, got %q", p.Origin)
		}
	}

	// Offset paging slices the same deterministic order.
	page2, total2, err := s.ListCommunityProductsPage(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if total2 != 2 || len(page2) != 1 || page2[0].ID != communityB.ID {
		t.Fatalf("offset page wrong: total=%d page=%+v", total2, page2)
	}
}

func TestSearchCommunityProducts(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	mk := func(typ, name string) {
		if _, err := s.CreateProduct(ctx, store.Product{Type: typ, Name: name, Origin: "community"}); err != nil {
			t.Fatal(err)
		}
	}
	mk("game", "Repro Alpha")
	mk("console", "Repro Console")
	// A provider product with a matching name must stay out of the lane.
	if _, err := s.CreateProduct(ctx, gameProduct(9310, 19, "Repro Provider", "SNES", "")); err != nil {
		t.Fatal(err)
	}

	got, err := s.SearchCommunityProducts(ctx, []string{"game"}, "repro", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "Repro Alpha" {
		t.Fatalf("game lane = %+v, want only the community game", got)
	}
	hw, err := s.SearchCommunityProducts(ctx, []string{"console", "accessory"}, "REPRO", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hw) != 1 || hw[0].Name != "Repro Console" {
		t.Fatalf("hardware lane (case-insensitive) = %+v", hw)
	}
	capped, err := s.SearchCommunityProducts(ctx, []string{"game"}, "repro", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 1 {
		t.Fatalf("cap ignored: %d results", len(capped))
	}
}

func TestPromoteProduct_FlipAndTwinGuard(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	c1, err := s.CreateProduct(ctx, store.Product{Type: "game", Name: "Promo A", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := s.CreateProduct(ctx, store.Product{Type: "game", Name: "Promo B", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}

	meta := store.IGDBMeta{GameID: 9401, Name: "Promo A", FetchedAt: time.Now().UTC()}
	plat := store.Platform{IGDBID: 19, Name: "SNES"}
	if err := s.PromoteProduct(ctx, c1.ID, &meta, &plat, nil); err != nil {
		t.Fatalf("promote: %v", err)
	}
	got, err := s.GetProduct(ctx, c1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin != "provider" || got.IGDB == nil || got.IGDB.GameID != 9401 {
		t.Fatalf("flip incomplete: origin=%q igdb=%+v", got.Origin, got.IGDB)
	}

	// The same unmatched identity slot is taken now: the index
	// adjudicates the second flip.
	if err := s.PromoteProduct(ctx, c2.ID, &meta, &plat, nil); !errors.Is(err, store.ErrIdentityTaken) {
		t.Fatalf("twin promote = %v, want ErrIdentityTaken", err)
	}
	// And nothing changed on the loser.
	still, err := s.GetProduct(ctx, c2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.Origin != "community" {
		t.Fatalf("failed promote must not flip: %q", still.Origin)
	}

	// A promoted product no longer matches the community guard;
	// re-promoting it reports ErrNotFound (handler translates via origin check).
	if err := s.PromoteProduct(ctx, c1.ID, &meta, &plat, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("re-promote = %v, want ErrNotFound", err)
	}
}

func TestPromoteCandidates_ReplaceListDismiss(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	a, err := s.CreateProduct(ctx, store.Product{Type: "game", Name: "Cand A", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateProduct(ctx, store.Product{Type: "console", Name: "Cand B", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}
	quiet, err := s.CreateProduct(ctx, store.Product{Type: "game", Name: "Cand Quiet", Origin: "community"})
	if err != nil {
		t.Fatal(err)
	}

	// Stored sorted best-first by the caller; index 0 is the sort key.
	if err := s.ReplacePromoteCandidates(ctx, a.ID, []store.PromoteCandidate{
		{Provider: "igdb", ProviderID: 1011, Name: "Chrono Trigger", Score: 0.92, FoundAt: now},
		{Provider: "igdb", ProviderID: 1018, Name: "Terranigma", Score: 0.80, FoundAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePromoteCandidates(ctx, b.ID, []store.PromoteCandidate{
		{Provider: "pricecharting", ProviderID: 5005, Name: "Super Mario 64", Score: 0.99, FoundAt: now},
	}); err != nil {
		t.Fatal(err)
	}

	page, total, err := s.ListPromoteCandidateProducts(ctx, 10, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(page) != 2 {
		t.Fatalf("total=%d len=%d, want 2/2 (quiet product excluded)", total, len(page))
	}
	if page[0].ID != b.ID || page[1].ID != a.ID {
		t.Fatalf("order must be strongest top candidate first: %s, %s", page[0].Name, page[1].Name)
	}
	_ = quiet

	only, total1, err := s.ListPromoteCandidateProducts(ctx, 10, 0, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if total1 != 1 || len(only) != 1 || only[0].ID != a.ID {
		t.Fatalf("product_id filter wrong: %d/%+v", total1, only)
	}

	// Dismiss drops the candidate and records the pair.
	if err := s.DismissPromoteCandidate(ctx, a.ID, "igdb", 1011); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProduct(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PromoteCandidates) != 1 || got.PromoteCandidates[0].ProviderID != 1018 {
		t.Fatalf("dismiss must pull the pair: %+v", got.PromoteCandidates)
	}
	if len(got.DismissedCandidates) != 1 || got.DismissedCandidates[0].ProviderID != 1011 {
		t.Fatalf("dismissed set wrong: %+v", got.DismissedCandidates)
	}

	// Empty replacement unsets the field (the product leaves the list).
	if err := s.ReplacePromoteCandidates(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, total, err = s.ListPromoteCandidateProducts(ctx, 10, 0, ""); err != nil || total != 1 {
		t.Fatalf("after unset total=%d err=%v, want 1", total, err)
	}

	// Origin guard: a provider product (e.g. one promoted mid-sweep) is
	// a silent no-op - no error, and no candidates land on it.
	prov, err := s.CreateProduct(ctx, gameProduct(9501, 19, "Prov Cand", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplacePromoteCandidates(ctx, prov.ID, []store.PromoteCandidate{
		{Provider: "igdb", ProviderID: 7777, Name: "Ghost", Score: 0.99, FoundAt: now},
	}); err != nil {
		t.Fatalf("provider-doc replace must be a silent no-op, got %v", err)
	}
	provGot, err := s.GetProduct(ctx, prov.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(provGot.PromoteCandidates) != 0 {
		t.Fatalf("no candidates may land on a provider doc: %+v", provGot.PromoteCandidates)
	}
}

func TestCommunityCover_RoundTrips(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	created, err := s.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Repro Cover", Origin: "community",
		Community: &store.CommunityMeta{PlatformName: "SNES", CoverURL: "https://img.example/rc.jpg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetProduct(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Community == nil || got.Community.CoverURL != "https://img.example/rc.jpg" {
		t.Fatalf("community cover did not round-trip: %+v", got.Community)
	}
}

// TestStore_ListCommunityRegionDocs pins the selection: community
// docs whose curated region is outside known, and only those (a
// regionless doc has nothing to fold, a known-region doc has nothing
// left to promote, a provider doc is out of scope).
func TestStore_ListCommunityRegionDocs(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	known := []string{"ntsc_u", "ntsc_j", "pal", "region_free", "korea", "brazil", "china"}

	jp, err := s.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Community JP", Origin: "community",
		Community: &store.CommunityMeta{Region: "Japan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	kr, err := s.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Community KR", Origin: "community",
		Community: &store.CommunityMeta{Region: "Korea"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Community No Region", Origin: "community",
	}); err != nil {
		t.Fatal(err)
	}
	// Already canonical: excluded from selection rather than re-fetched
	// and re-folded to itself every run.
	if _, err := s.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Community Already Canonical", Origin: "community",
		Community: &store.CommunityMeta{Region: "ntsc_j"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProduct(ctx, gameProduct(9601, 19, "Provider Excluded", "SNES", "")); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListCommunityRegionDocs(ctx, known)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]string, len(got))
	for _, ref := range got {
		byID[ref.ID] = ref.Region
	}
	if len(byID) != 2 || byID[jp.ID] != "Japan" || byID[kr.ID] != "Korea" {
		t.Fatalf("got %+v, want exactly {%s: Japan, %s: Korea}", byID, jp.ID, kr.ID)
	}
}

// TestStore_SetCommunityRegion pins the write: curated region
// rewrites to canonical form and updated_at bumps.
func TestStore_SetCommunityRegion(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Community Region Rewrite", Origin: "community",
		Community: &store.CommunityMeta{Region: "Japan"},
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // guards the updated_at bump against a same-millisecond tie

	if err := s.SetCommunityRegion(ctx, created.ID, "ntsc_j"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetProduct(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Community == nil || got.Community.Region != "ntsc_j" {
		t.Fatalf("region = %+v, want ntsc_j", got.Community)
	}
	if !got.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at did not bump: before=%v after=%v", created.UpdatedAt, got.UpdatedAt)
	}
}

// TestStore_SetCommunityRegion_OriginLeftCommunityIsNoOp pins the
// race guard: a doc promoted to provider between list and write must
// not have community.region rewritten (would be silent residue).
func TestStore_SetCommunityRegion_OriginLeftCommunityIsNoOp(t *testing.T) {
	s, mdb := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateProduct(ctx, store.Product{
		Type: "game", Name: "Community Region Race", Origin: "community",
		Community: &store.CommunityMeta{Region: "Japan"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Stand-in for a promote landing mid-write: flip origin directly
	// rather than exercising the real promote path (irrelevant here).
	if _, err := mdb.Collection("products").UpdateByID(ctx, created.ID,
		bson.D{{Key: "$set", Value: bson.D{{Key: "origin", Value: "provider"}}}}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetCommunityRegion(ctx, created.ID, "ntsc_j"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetProduct(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Community == nil || got.Community.Region != "Japan" {
		t.Fatalf("region = %+v, want unchanged Japan (origin left community, write must no-op)", got.Community)
	}
}
