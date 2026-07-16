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
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/levonn-dev/vg-collect/services/enrichment/internal/db"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/store"
	"github.com/levonn-dev/vg-collect/services/enrichment/migrations"
)

// newTestStore boots a migrated MongoDB and returns the Store plus the
// raw database handle for direct assertions. Skipped under -short.
func newTestStore(t *testing.T) (*store.Store, *mongo.Database) {
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
	url, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, url, "enrichment", migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	client, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	mdb := client.Database("enrichment")
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
// platform, listing), the unmatched member (no mapping) is the
// family's singleton, and variant text plays no identity role.
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

// The find-or-create race: concurrent resolves of one identity must
// converge on a single product. This is a genuine guard - without the
// unique identity index the count would exceed 1, and without the
// duplicate-key re-find CreateProduct would surface errors.
func TestProduct_ConcurrentCreate_SingleWinner(t *testing.T) {
	s, mdb := newTestStore(t)
	ctx := context.Background()

	const n = 10
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := s.CreateProduct(ctx, gameProduct(1013, 7, "Final Fantasy VII", "PlayStation", ""))
			ids[i], errs[i] = p.ID, err
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
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

func TestProduct_SetPriceChartingIfMissing_FillsExactlyOnce(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	created, err := s.CreateProduct(ctx, gameProduct(1018, 19, "Terranigma", "Super Nintendo Entertainment System", ""))
	if err != nil {
		t.Fatal(err)
	}

	loose := int64(9800)
	meta := &store.PCMeta{
		PCProductID: 7001, PCName: "Terranigma [PAL]", ConsoleName: "PAL Super Nintendo",
		MatchConfidence: 1.0, Verified: false,
		Current: store.PriceQuote{LooseCents: &loose},
		AsOf:    time.Now().UTC().Truncate(time.Millisecond),
	}
	landed, err := s.SetPriceChartingIfMissing(ctx, created.ID, meta)
	if err != nil || !landed {
		t.Fatalf("first fill must land: landed=%v err=%v", landed, err)
	}
	got, err := s.GetProduct(ctx, created.ID)
	if err != nil || got.PriceCharting == nil || got.PriceCharting.PCProductID != 7001 {
		t.Fatalf("fill must persist the mapping: %+v, %v", got.PriceCharting, err)
	}

	// Mapped now: a second fill must not land and must change nothing.
	// This is the guard that fails if the conditional filter is ever
	// swapped for the unconditional SetPriceCharting.
	other := &store.PCMeta{PCProductID: 7002, PCName: "Wrong", ConsoleName: "Wrong",
		MatchConfidence: 1.0, Verified: false, AsOf: meta.AsOf}
	landed, err = s.SetPriceChartingIfMissing(ctx, created.ID, other)
	if err != nil || landed {
		t.Fatalf("second fill must not land: landed=%v err=%v", landed, err)
	}
	got, err = s.GetProduct(ctx, created.ID)
	if err != nil || got.PriceCharting.PCProductID != 7001 {
		t.Fatalf("existing mapping must be untouched: %+v, %v", got.PriceCharting, err)
	}

	// A mapping cleared by the admin path is missing again, but the
	// clear also holds it (SetPriceCharting's own $unset/$set pair,
	// untouched by this method): the fill must not land. The hold-lift
	// walkthrough lives in TestProduct_MappingWritesHoldAndIdentityTaken.
	if err := s.SetPriceCharting(ctx, created.ID, nil); err != nil {
		t.Fatal(err)
	}
	landed, err = s.SetPriceChartingIfMissing(ctx, created.ID, other)
	if err != nil || landed {
		t.Fatalf("a held clear must not be fillable: landed=%v err=%v", landed, err)
	}

	// Unknown product id: no landing, no error.
	landed, err = s.SetPriceChartingIfMissing(ctx, "00000000-0000-0000-0000-000000000000", meta)
	if err != nil || landed {
		t.Fatalf("unknown id: landed=%v err=%v", landed, err)
	}
}

// Mapping writes are identity moves now: a taken (game, platform,
// listing) slot surfaces ErrIdentityTaken instead of a generic error,
// clears set the walk hold, and any set lifts it.
func TestProduct_MappingWritesHoldAndIdentityTaken(t *testing.T) {
	s, mdb := newTestStore(t)
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

	// Filling the unmatched member with a taken listing is refused.
	landed, err := s.SetPriceChartingIfMissing(ctx, unmatched.ID, meta(5005))
	if landed || !errors.Is(err, store.ErrIdentityTaken) {
		t.Fatalf("want ErrIdentityTaken, got landed=%v err=%v", landed, err)
	}

	// Clearing the matched member collides with the existing unmatched
	// member (two null keys in one family).
	if err := s.SetPriceCharting(ctx, matched.ID, nil); !errors.Is(err, store.ErrIdentityTaken) {
		t.Fatalf("clear onto an occupied null slot: want ErrIdentityTaken, got %v", err)
	}

	// A held product is skipped by the fill: the walk must never undo
	// a deliberate admin clear that lands mid-walk, even one landing
	// after the walk already holds this product in its worklist.
	if _, err := mdb.Collection("products").UpdateByID(ctx, unmatched.ID,
		bson.D{{Key: "$set", Value: bson.D{{Key: "match_hold", Value: true}}}}); err != nil {
		t.Fatal(err)
	}
	landed, err = s.SetPriceChartingIfMissing(ctx, unmatched.ID, meta(5099))
	if err != nil || landed {
		t.Fatalf("held product must not be filled: landed=%v err=%v", landed, err)
	}
	got, err := s.GetProduct(ctx, unmatched.ID)
	if err != nil || got.PriceCharting != nil || !got.MatchHold {
		t.Fatalf("held product must stay unmatched and held: %+v, %v", got, err)
	}

	// Lifting the hold (as the admin's own set path would) makes the
	// same fill land.
	if _, err := mdb.Collection("products").UpdateByID(ctx, unmatched.ID,
		bson.D{{Key: "$unset", Value: bson.D{{Key: "match_hold", Value: ""}}}}); err != nil {
		t.Fatal(err)
	}
	landed, err = s.SetPriceChartingIfMissing(ctx, unmatched.ID, meta(5099))
	if err != nil || !landed {
		t.Fatalf("fill must land once the hold lifts: landed=%v err=%v", landed, err)
	}
	got, err = s.GetProduct(ctx, unmatched.ID)
	if err != nil || got.PriceCharting == nil || got.PriceCharting.PCProductID != 5099 || got.MatchHold {
		t.Fatalf("fill must persist: %+v, %v", got, err)
	}

	// The admin clear is now free (the family's null slot emptied when
	// the fill landed) and sets the hold; a following set lifts it.
	if err := s.SetPriceCharting(ctx, matched.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetProduct(ctx, matched.ID)
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

// The walk's worklist: unmatched games only, holds excluded, oldest
// updated_at first, capped.
func TestProduct_ListUnmatchedGames(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	oldest, err := s.CreateProduct(ctx, gameProduct(1011, 19, "Chrono Trigger", "Super Nintendo Entertainment System", ""))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	newest, err := s.CreateProduct(ctx, gameProduct(1005, 4, "Super Mario 64", "Nintendo 64", ""))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	held, err := s.CreateProduct(ctx, gameProduct(1014, 19, "Final Fantasy VI", "Super Nintendo Entertainment System", ""))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPriceCharting(ctx, held.ID, nil); err != nil { // clear = hold
		t.Fatal(err)
	}
	m := gameProduct(1013, 7, "Final Fantasy VII", "PlayStation", "")
	m.PriceCharting = &store.PCMeta{PCProductID: 5013, PCName: "Final Fantasy VII", ConsoleName: "Playstation", MatchConfidence: 1, AsOf: time.Now().UTC()}
	if _, err := s.CreateProduct(ctx, m); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListUnmatchedGames(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != oldest.ID || got[1].ID != newest.ID {
		t.Fatalf("want [oldest newest] without held/matched, got %+v", got)
	}
	capped, err := s.ListUnmatchedGames(ctx, 1)
	if err != nil || len(capped) != 1 || capped[0].ID != oldest.ID {
		t.Fatalf("cap must keep the oldest: %+v, %v", capped, err)
	}
}

// The reprojection walk's worklist: ListIGDBProducts returns every
// igdb-bearing product - including ones that already carry a release
// table (the walk reprojects them from raw and writes only on a diff,
// so a pre-feature filter would wrongly skip an already-healed product
// that a projection-logic change now needs to revisit) - excludes
// hardware (no igdb subdoc), sorts by _id ascending, and is uncapped: a
// full nightly sweep, mirroring ListPriced's posture.
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
				// A populated release table: the retired missing-dates
				// query skipped this shape; the reprojection walk must not.
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
// predates the release-dates feature: the release_dates key is
// entirely absent (not null, not an empty array). SetIGDB cannot
// produce this shape anymore (it always writes the field post-feature),
// so this bypasses the store's write path via the raw collection
// handle, following the same direct-insert pattern as
// TestMigration_ListingKeyedIdentityDeletesTupleResidue's seed helper.
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

	hits, err := s.SearchByName(ctx, "chrono", 10)
	if err != nil || len(hits) != 2 {
		t.Fatalf("search: %d, %v", len(hits), err)
	}
	if hits[0].Name != "Chrono Cross" {
		t.Fatalf("want name-sorted results, got %s first", hits[0].Name)
	}
	one, _ := s.SearchByName(ctx, "CHRONO", 1)
	if len(one) != 1 {
		t.Fatal("limit or case-insensitivity broken")
	}
}

func TestSnapshotsSinceWindowsAndOrders(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	base := time.Date(2026, time.July, 1, 12, 0, 0, 0, time.UTC)
	cents := func(v int64) *int64 { return &v }

	// Two products; product A has an old point outside the window, two
	// inside (inserted newest-first to prove the read sorts), product B
	// one point. A third id is never written.
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

// The JP-twin fold: a Super Famicom (58) row is visible on a SNES (19)
// product and vice versa; Family Computer (99) folds into NES (18) both
// ways; an unrelated platform's row never folds; a platform-0 row is
// dropped even when it would be the earliest; and the scalar is the
// earliest across the folded set.
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

// SameProjection is the reprojection walk's diff gate: two projections
// that differ only by FetchedAt are the same; differing rows are not;
// and a nil release table is NOT the same as an empty one (that exact
// difference is a pre-feature projection due for a rebuild).
func TestIGDBMeta_SameProjection(t *testing.T) {
	base := store.IGDBMeta{
		GameID: 1011, Name: "Chrono Trigger",
		Genres:       []store.Genre{{ID: 12, Name: "Role-playing (RPG)"}},
		Themes:       []string{"Fantasy"},
		Franchises:   []string{"Chrono"},
		SimilarGames: []int64{1012},
		Companies:    []store.Company{{Name: "Square", Developer: true, Publisher: true}},
		ReleaseDates: []store.MetaReleaseDate{{Region: "japan", Date: time.Unix(794880000, 0).UTC()}},
		FetchedAt:    time.Unix(1000, 0).UTC(),
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
}

// The listing-keyed migration deletes only game products whose
// region/edition/variant forked identity (raw-API dev residue);
// clean game products and hardware survive with their ids, and the
// rebuilt index enforces the family singleton.
func TestMigration_ListingKeyedIdentityDeletesTupleResidue(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	mc, err := tcmongo.Run(ctx, "mongo:8")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mc.Terminate(ctx) })
	url, err := mc.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	client, err := db.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })

	driver, err := mongodriver.WithInstance(client, &mongodriver.Config{
		DatabaseName: "enrichment",
		Locking:      mongodriver.Locking{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "enrichment", driver)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(3); err != nil {
		t.Fatalf("migrate to pre-listing-keyed state: %v", err)
	}

	col := client.Database("enrichment").Collection("products")
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

	if err := m.Up(); err != nil {
		t.Fatalf("migrate up: %v", err)
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
