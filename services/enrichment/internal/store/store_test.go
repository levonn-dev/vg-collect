package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
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

func TestProduct_VariantIsDistinctIdentity(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	a, err := s.CreateProduct(ctx, gameProduct(1011, 19, "Chrono Trigger", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateProduct(ctx, gameProduct(1011, 19, "Chrono Trigger", "SNES", "cart only repro sticker"))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("distinct variants must be distinct products")
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

	// A mapping cleared by the admin path ($unset) is missing again:
	// fillable.
	if err := s.SetPriceCharting(ctx, created.ID, nil); err != nil {
		t.Fatal(err)
	}
	landed, err = s.SetPriceChartingIfMissing(ctx, created.ID, other)
	if err != nil || !landed {
		t.Fatalf("cleared mapping must be fillable: landed=%v err=%v", landed, err)
	}

	// Unknown product id: no landing, no error.
	landed, err = s.SetPriceChartingIfMissing(ctx, "00000000-0000-0000-0000-000000000000", meta)
	if err != nil || landed {
		t.Fatalf("unknown id: landed=%v err=%v", landed, err)
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
	m := store.NewIGDBMeta(g, at)
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
