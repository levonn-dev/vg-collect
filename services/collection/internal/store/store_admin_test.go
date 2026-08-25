// Tests for admin and maintenance levers: catalog resnapshot and rematch
// refs, platform and region normalization, and user-data purge.

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

// TestListGameBackedRefs seeds product-backed game/hardware/custom entries for
// two users plus a second game-backed entry sharing userA's product_id
// (different region), and asserts the walk sees exactly the game-backed rows
// from both users (deliberately unscoped), ordered product_id then id
// (tie-break exercised), with each row's region and localized trio read back correctly.
func TestListGameBackedRefs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()

	// seedTrio creates a product-backed game (a), hardware (b), and custom (c)
	// entry for one user, returning a; name/translit/cover seed a's localized
	// trio (nil for callers that don't care), exercising both round trips.
	seedTrio := func(user uuid.UUID, released time.Time, name, translit, cover *string) store.Entry {
		game := baseEntry(user)
		game.IGDBGameID = new(int64(1000))
		game.FirstReleaseDate = &released
		game.LocalizedName = name
		game.LocalizedNameTranslit = translit
		game.LocalizedCoverURL = cover
		gameEntry := mustCreate(t, s, game, nil)

		hardware := baseEntry(user)
		hardware.ItemType = "console" // product-backed; igdb_game_id stays nil (baseEntry default)
		mustCreate(t, s, hardware, nil)

		mustCreate(t, s, customEntry(user), nil) // product_id nil

		return gameEntry
	}

	dateA := time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)
	dateA2 := time.Date(1996, time.January, 1, 0, 0, 0, 0, time.UTC)
	dateB := time.Date(1998, time.November, 21, 0, 0, 0, 0, time.UTC)
	jaName, jaTranslit, jaCover := new("クロノ・トリガー"), new("Kurono Torigaa"), new("https://x/ja-cover.jpg")
	gameA := seedTrio(userA, dateA, jaName, jaTranslit, jaCover)
	gameB := seedTrio(userB, dateB, nil, nil, nil)

	// A second game-backed entry on gameA's SAME product_id, with a different
	// region (pal): gives two rows an identical product_id so the id tie-break
	// in ORDER BY gets exercised, and pins per-entry regions.
	gameA2 := baseEntry(userA)
	gameA2.ProductID = gameA.ProductID
	gameA2.Region = "pal"
	gameA2.IGDBGameID = new(int64(1000))
	gameA2.FirstReleaseDate = &dateA2
	gameA2.Developers = []string{"Square"}
	gameA2.Publishers = []string{"Square", "Nintendo"}
	gameA2Entry := mustCreate(t, s, gameA2, nil)

	refs, err := s.ListGameBackedRefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 {
		t.Fatalf("expected exactly the 3 game-backed rows (hardware/custom excluded), got %d: %+v", len(refs), refs)
	}

	// Ordering: product_id then id, checked pairwise so gameA/gameA2 (same
	// product_id) exercises the id tie-break. Postgres orders uuid by raw
	// bytes, which agrees with String()'s byte order (fixed hyphen positions),
	// a faithful proxy for SQL order.
	for i := 1; i < len(refs); i++ {
		prev, cur := refs[i-1], refs[i]
		if prev.ProductID.String() > cur.ProductID.String() {
			t.Fatalf("not ordered by product_id: %+v", refs)
		}
		if prev.ProductID == cur.ProductID && prev.EntryID.String() > cur.EntryID.String() {
			t.Fatalf("rows sharing a product_id must tie-break by id: %+v", refs)
		}
	}

	strEq := func(a, b *string) bool {
		if a == nil || b == nil {
			return a == b
		}
		return *a == *b
	}
	strsEq := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	want := map[uuid.UUID]struct {
		productID              uuid.UUID
		region                 string
		release                time.Time
		name, translit, cover  *string
		developers, publishers []string
	}{
		gameA.ID:       {*gameA.ProductID, "ntsc_u", dateA, jaName, jaTranslit, jaCover, nil, nil},
		gameA2Entry.ID: {*gameA.ProductID, "pal", dateA2, nil, nil, nil, []string{"Square"}, []string{"Square", "Nintendo"}},
		gameB.ID:       {*gameB.ProductID, "ntsc_u", dateB, nil, nil, nil, nil, nil},
	}
	for _, r := range refs {
		w, ok := want[r.EntryID]
		if !ok {
			t.Fatalf("unexpected entry in refs: %+v", r)
		}
		if r.ProductID != w.productID {
			t.Fatalf("product_id for %s: got %s, want %s", r.EntryID, r.ProductID, w.productID)
		}
		if r.Region != w.region {
			t.Fatalf("region for %s: got %q, want %q", r.EntryID, r.Region, w.region)
		}
		if r.FirstReleaseDate == nil || !r.FirstReleaseDate.Equal(w.release) {
			t.Fatalf("first_release_date for %s: got %v, want %v", r.EntryID, r.FirstReleaseDate, w.release)
		}
		if !strEq(r.LocalizedName, w.name) {
			t.Fatalf("localized_name for %s: got %v, want %v", r.EntryID, r.LocalizedName, w.name)
		}
		if !strEq(r.LocalizedNameTranslit, w.translit) {
			t.Fatalf("localized_name_translit for %s: got %v, want %v", r.EntryID, r.LocalizedNameTranslit, w.translit)
		}
		if !strEq(r.LocalizedCoverURL, w.cover) {
			t.Fatalf("localized_cover_url for %s: got %v, want %v", r.EntryID, r.LocalizedCoverURL, w.cover)
		}
		if !strsEq(r.Developers, w.developers) || !strsEq(r.Publishers, w.publishers) {
			t.Fatalf("credits for %s: got %v/%v, want %v/%v",
				r.EntryID, r.Developers, r.Publishers, w.developers, w.publishers)
		}
	}
}

// TestSetSnapshotFields covers the resnapshot walk's only write: it rewrites
// date and localized trio in one UPDATE; all-nil arguments clear every column to NULL.
func TestSetSnapshotFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	e := baseEntry(user)
	e.IGDBGameID = new(int64(1000))
	e.FirstReleaseDate = new(time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC))
	created := mustCreate(t, s, e, nil)

	newDate := time.Date(1995, time.August, 22, 0, 0, 0, 0, time.UTC)
	name, translit, cover := new("クロノ・トリガー"), new("Kurono Torigaa"), new("https://x/ja-cover.jpg")
	devs, pubs := []string{"Square"}, []string{"Square", "Nintendo"}
	if err := s.SetSnapshotFields(ctx, created.ID, &newDate, name, translit, cover, devs, pubs); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEntry(ctx, user, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FirstReleaseDate == nil || !got.FirstReleaseDate.Equal(newDate) {
		t.Fatalf("date: got %v, want %v", got.FirstReleaseDate, newDate)
	}
	if got.LocalizedName == nil || *got.LocalizedName != *name {
		t.Fatalf("localized_name: got %v, want %v", got.LocalizedName, *name)
	}
	if got.LocalizedNameTranslit == nil || *got.LocalizedNameTranslit != *translit {
		t.Fatalf("localized_name_translit: got %v, want %v", got.LocalizedNameTranslit, *translit)
	}
	if got.LocalizedCoverURL == nil || *got.LocalizedCoverURL != *cover {
		t.Fatalf("localized_cover_url: got %v, want %v", got.LocalizedCoverURL, *cover)
	}
	if len(got.Developers) != 1 || got.Developers[0] != "Square" {
		t.Fatalf("developers: got %v, want [Square]", got.Developers)
	}
	if len(got.Publishers) != 2 || got.Publishers[0] != "Square" || got.Publishers[1] != "Nintendo" {
		t.Fatalf("publishers: got %v, want [Square Nintendo]", got.Publishers)
	}
	if !got.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at must move: %v -> %v", created.UpdatedAt, got.UpdatedAt)
	}

	if err := s.SetSnapshotFields(ctx, created.ID, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.GetEntry(ctx, user, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.FirstReleaseDate != nil || cleared.LocalizedName != nil ||
		cleared.LocalizedNameTranslit != nil || cleared.LocalizedCoverURL != nil ||
		cleared.Developers != nil || cleared.Publishers != nil {
		t.Fatalf("all six columns must clear to NULL, got %+v", cleared)
	}
}

// TestListAutoGameRematchRefs covers the rematch's row source: only
// auto-priced, game-backed entries with a platform id are listed, ordered so
// (game, platform, region) grouping is contiguous.
func TestListAutoGameRematchRefs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	// (a) auto game-backed, platform id present -> listed.
	a := baseEntry(user)
	a.IGDBGameID = new(int64(1000))
	a.PlatformIGDBID = new(int64(6))
	a.PlatformName = new("SNES")
	dateA := time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC)
	a.FirstReleaseDate = &dateA
	a.LocalizedName = new("クロノ・トリガー")
	a.LocalizedNameTranslit = new("Kurono Torigaa")
	a.LocalizedCoverURL = new("https://x/ja-cover.jpg")
	entryA := mustCreate(t, s, a, nil)

	// (b) pricing_mode proxy, otherwise same shape as (a): excluded by the
	// auto-only filter even with igdb_game_id/platform_igdb_id both present.
	b := baseEntry(user)
	b.IGDBGameID = new(int64(2000))
	b.PlatformIGDBID = new(int64(7))
	b.PlatformName = new("Genesis")
	b.PricingMode = "proxy"
	b.PricingProductID = new(uuid.New())
	mustCreate(t, s, b, nil)

	// (c) custom entry (nil product_id) -> excluded.
	mustCreate(t, s, customEntry(user), nil)

	// (d) auto game-backed, same game+platform as (a) but a second region:
	// listed, giving the ORDER BY's region component something to sort
	// (a and d share igdb_game_id/platform_igdb_id).
	d := baseEntry(user)
	d.IGDBGameID = new(int64(1000))
	d.PlatformIGDBID = new(int64(6))
	d.PlatformName = new("SNES")
	d.Region = "pal"
	dateD := time.Date(1996, time.January, 1, 0, 0, 0, 0, time.UTC)
	d.FirstReleaseDate = &dateD
	entryD := mustCreate(t, s, d, nil)

	// (e) same shape as (a) but a user-picked match: excluded even though
	// nothing else about the row fails the other predicates.
	e := baseEntry(user)
	e.IGDBGameID = new(int64(1000))
	e.PlatformIGDBID = new(int64(6))
	e.PlatformName = new("SNES")
	e.MatchProvenance = "user"
	mustCreate(t, s, e, nil)

	refs, err := s.ListAutoGameRematchRefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected exactly a and d (proxy, custom, and user-picked excluded), got %d: %+v", len(refs), refs)
	}

	// ntsc_u sorts before pal, so a's row must precede d's: ordering keys off
	// region, not just igdb_game_id/platform_igdb_id (both rows share those).
	if refs[0].EntryID != entryA.ID || refs[1].EntryID != entryD.ID {
		t.Fatalf("must be ordered (igdb_game_id, platform_igdb_id, region, id): %+v", refs)
	}

	got := refs[0]
	if got.ProductID != *entryA.ProductID || got.IGDBGameID != 1000 || got.PlatformIGDBID != 6 || got.Region != "ntsc_u" {
		t.Fatalf("a's identity fields: %+v", got)
	}
	if got.FirstReleaseDate == nil || !got.FirstReleaseDate.Equal(dateA) {
		t.Fatalf("a's first_release_date: %v", got.FirstReleaseDate)
	}
	if got.LocalizedName == nil || *got.LocalizedName != *a.LocalizedName ||
		got.LocalizedNameTranslit == nil || *got.LocalizedNameTranslit != *a.LocalizedNameTranslit ||
		got.LocalizedCoverURL == nil || *got.LocalizedCoverURL != *a.LocalizedCoverURL {
		t.Fatalf("a's stored snapshot trio: %+v", got)
	}

	got = refs[1]
	if got.ProductID != *entryD.ProductID || got.IGDBGameID != 1000 || got.PlatformIGDBID != 6 || got.Region != "pal" {
		t.Fatalf("d's identity fields: %+v", got)
	}
	if got.FirstReleaseDate == nil || !got.FirstReleaseDate.Equal(dateD) {
		t.Fatalf("d's first_release_date: %v", got.FirstReleaseDate)
	}
	if got.LocalizedName != nil || got.LocalizedNameTranslit != nil || got.LocalizedCoverURL != nil {
		t.Fatalf("d seeded no localized trio, must read back nil: %+v", got)
	}
}

// TestRepointEntry covers the rematch's only write: moving an entry to a
// sibling member and rewriting its product-derived snapshot in one statement.
func TestRepointEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	e := baseEntry(user)
	e.IGDBGameID = new(int64(1000))
	e.PlatformIGDBID = new(int64(6))
	e.PlatformName = new("SNES")
	created := mustCreate(t, s, e, nil)

	newProduct := uuid.New()
	newDate := time.Date(1996, time.March, 6, 0, 0, 0, 0, time.UTC)
	name, translit, cover := new("聖剣伝説3"), new("Seiken Densetsu 3"), new("https://x/jp-cover.jpg")
	devs, pubs := []string{"Square"}, []string{"Square"}
	if err := s.RepointEntry(ctx, created.ID, newProduct, &newDate, name, translit, cover, devs, pubs); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetEntry(ctx, user, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProductID == nil || *got.ProductID != newProduct {
		t.Fatalf("product_id: got %v, want %v", got.ProductID, newProduct)
	}
	if got.FirstReleaseDate == nil || !got.FirstReleaseDate.Equal(newDate) {
		t.Fatalf("first_release_date: got %v, want %v", got.FirstReleaseDate, newDate)
	}
	if got.LocalizedName == nil || *got.LocalizedName != *name {
		t.Fatalf("localized_name: got %v, want %v", got.LocalizedName, *name)
	}
	if got.LocalizedNameTranslit == nil || *got.LocalizedNameTranslit != *translit {
		t.Fatalf("localized_name_translit: got %v, want %v", got.LocalizedNameTranslit, *translit)
	}
	if got.LocalizedCoverURL == nil || *got.LocalizedCoverURL != *cover {
		t.Fatalf("localized_cover_url: got %v, want %v", got.LocalizedCoverURL, *cover)
	}
	if len(got.Developers) != 1 || got.Developers[0] != "Square" ||
		len(got.Publishers) != 1 || got.Publishers[0] != "Square" {
		t.Fatalf("credits: got %v/%v, want [Square]/[Square]", got.Developers, got.Publishers)
	}
	if !got.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at must move: %v -> %v", created.UpdatedAt, got.UpdatedAt)
	}
}

func TestPurgeUserData_RemovesExactlyOneUsersRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()

	aTag, err := s.CreateTag(ctx, a, "rpg")
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, baseEntry(a), []uuid.UUID{aTag.ID})
	mustCreate(t, s, baseEntry(a), nil)
	if _, err := s.CreateView(ctx, a, "Backlog", []byte(`{}`), "private"); err != nil {
		t.Fatal(err)
	}

	bTag, err := s.CreateTag(ctx, b, "rpg")
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, baseEntry(b), []uuid.UUID{bTag.ID})
	if _, err := s.CreateView(ctx, b, "Backlog", []byte(`{}`), "private"); err != nil {
		t.Fatal(err)
	}

	if err := s.PurgeUserData(ctx, a); err != nil {
		t.Fatal(err)
	}

	aEntries, err := s.ListEntries(ctx, a, store.Filters{})
	if err != nil || len(aEntries) != 0 {
		t.Fatalf("a's entries must be gone: %+v %v", aEntries, err)
	}
	aTags, err := s.ListTags(ctx, a)
	if err != nil || len(aTags) != 0 {
		t.Fatalf("a's tags must be gone: %+v %v", aTags, err)
	}
	aViews, err := s.ListViews(ctx, a)
	if err != nil || len(aViews) != 0 {
		t.Fatalf("a's views must be gone: %+v %v", aViews, err)
	}

	bEntries, err := s.ListEntries(ctx, b, store.Filters{})
	if err != nil || len(bEntries) != 1 {
		t.Fatalf("b's entries must survive: %+v %v", bEntries, err)
	}
	bTags, err := s.ListTags(ctx, b)
	if err != nil || len(bTags) != 1 {
		t.Fatalf("b's tags must survive: %+v %v", bTags, err)
	}
	bViews, err := s.ListViews(ctx, b)
	if err != nil || len(bViews) != 1 {
		t.Fatalf("b's views must survive: %+v %v", bViews, err)
	}

	if err := s.PurgeUserData(ctx, a); err != nil {
		t.Fatalf("purging an already-empty collection must be a no-op: %v", err)
	}
}

func TestCountEntriesByProduct(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	productID := uuid.New()

	// Two entries on the counted product across DIFFERENT users (count is
	// catalog-wide, not caller-scoped), plus unrelated noise that must not count.
	for _, user := range []uuid.UUID{uuid.New(), uuid.New()} {
		e := baseEntry(user)
		e.ProductID = new(productID)
		mustCreate(t, s, e, nil)
	}
	mustCreate(t, s, baseEntry(uuid.New()), nil)
	mustCreate(t, s, customEntry(uuid.New()), nil)

	n, err := s.CountEntriesByProduct(ctx, productID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("count = %d, want 2", n)
	}

	none, err := s.CountEntriesByProduct(ctx, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if none != 0 {
		t.Fatalf("unreferenced product count = %d, want 0", none)
	}
}

func TestNormalizePlatformStore_SelectAndStamp(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()

	// A custom entry with a free-text platform, no igdb id: selected.
	free := customEntry(userID)
	name := "snes"
	free.PlatformName = &name
	free.PlatformIGDBID = nil
	created, err := s.CreateEntry(ctx, free, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A custom entry with a platform id already: NOT selected.
	stamped := customEntry(userID)
	pn := "SNES"
	pid := int64(19)
	stamped.PlatformName = &pn
	stamped.PlatformIGDBID = &pid
	if _, err := s.CreateEntry(ctx, stamped, nil); err != nil {
		t.Fatal(err)
	}

	refs, err := s.ListNameOnlyPlatformEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].EntryID != created.ID || refs[0].PlatformName != "snes" {
		t.Fatalf("selection wrong: %+v", refs)
	}

	if err := s.SetEntryPlatformIdentity(ctx, created.ID, 19, "SNES"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.PlatformIGDBID == nil || *got.PlatformIGDBID != 19 || got.PlatformName == nil || *got.PlatformName != "SNES" {
		t.Fatalf("stamp wrong: pid=%v name=%v", got.PlatformIGDBID, got.PlatformName)
	}
	// Re-run: the stamped row leaves the selection set.
	refs2, err := s.ListNameOnlyPlatformEntries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs2) != 0 {
		t.Fatalf("re-run still selects %d rows, want 0", len(refs2))
	}
}

// TestListOpenRegionEntries covers the normalize-regions selection: entries whose region sits outside the known set.
func TestListOpenRegionEntries(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	known := []string{"ntsc_u", "ntsc_j", "pal", "region_free"}

	open := insertEntryWithRegion(t, s, "Korea")
	insertEntryWithRegion(t, s, "ntsc_j") // in the known set: not selected

	refs, err := s.ListOpenRegionEntries(ctx, known)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].EntryID != open.ID || refs[0].Region != "Korea" {
		t.Fatalf("refs = %+v, want the one Korea row (id %s)", refs, open.ID)
	}
	if refs[0].ProductID == nil || *refs[0].ProductID != *open.ProductID {
		t.Fatalf("product_id not carried: %+v", refs[0])
	}
}

// TestListOpenRegionEntries_EmptyKnownSelectsAll pins the degenerate case in
// NOT (region = ANY($1)): an empty slice matches nothing under ANY, so NOT
// flips every row to selected, a trap for a future caller passing an empty known set.
func TestListOpenRegionEntries_EmptyKnownSelectsAll(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	known := insertEntryWithRegion(t, s, "ntsc_j")
	free := insertEntryWithRegion(t, s, "Korea")

	refs, err := s.ListOpenRegionEntries(ctx, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("refs = %+v, want both entries selected with an empty known set", refs)
	}
	got := map[uuid.UUID]bool{}
	for _, r := range refs {
		got[r.EntryID] = true
	}
	if !got[known.ID] || !got[free.ID] {
		t.Fatalf("refs = %+v, want both the known-valued %s and the free-text %s", refs, known.ID, free.ID)
	}
}

// TestPromoteEntryRegion_ClearsAck covers the plain write: canonicalizing the
// region clears any region-mismatch ack, the same fresh-choice rule RepointEntry uses.
func TestPromoteEntryRegion_ClearsAck(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	e := baseEntry(userID)
	e.Region = "Japan"
	created := mustCreate(t, s, e, nil)
	if err := s.AckRegionMismatch(ctx, userID, created.ID); err != nil {
		t.Fatal(err)
	}

	if err := s.PromoteEntryRegion(ctx, created.ID, "ntsc_j"); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Region != "ntsc_j" {
		t.Fatalf("region: got %q, want ntsc_j", got.Region)
	}
	if got.RegionMismatchAckAt != nil {
		t.Fatal("promote must clear region_mismatch_ack_at")
	}
}

// TestPromoteEntryRegionSnapshot covers the other write: the igdb-backed arm
// re-picks the product-derived snapshot in the same statement as
// canonicalization, since the promoted region may unlock a localization
// chain the free-text value never had.
func TestPromoteEntryRegionSnapshot(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	e := baseEntry(userID)
	e.Region = "Japan"
	e.IGDBGameID = new(int64(1000))
	created := mustCreate(t, s, e, nil)
	if created.LocalizedName != nil || created.LocalizedNameTranslit != nil || created.LocalizedCoverURL != nil {
		t.Fatal("fixture must start with a null localized trio")
	}

	d := time.Date(1995, time.September, 30, 0, 0, 0, 0, time.UTC)
	name, translit, cover := new("聖剣伝説3"), new("Seiken Densetsu 3"), new("https://x/jp-cover.jpg")
	if err := s.PromoteEntryRegionSnapshot(ctx, created.ID, "ntsc_j", &d, name, translit, cover); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Region != "ntsc_j" {
		t.Fatalf("region: got %q, want ntsc_j", got.Region)
	}
	if got.FirstReleaseDate == nil || !got.FirstReleaseDate.Equal(d) {
		t.Fatalf("first_release_date: got %v, want %v", got.FirstReleaseDate, d)
	}
	if got.LocalizedName == nil || *got.LocalizedName != *name {
		t.Fatalf("localized_name: got %v, want %v", got.LocalizedName, *name)
	}
	if got.LocalizedNameTranslit == nil || *got.LocalizedNameTranslit != *translit {
		t.Fatalf("localized_name_translit: got %v, want %v", got.LocalizedNameTranslit, *translit)
	}
	if got.LocalizedCoverURL == nil || *got.LocalizedCoverURL != *cover {
		t.Fatalf("localized_cover_url: got %v, want %v", got.LocalizedCoverURL, *cover)
	}
}
