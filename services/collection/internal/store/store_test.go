package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
	"github.com/levonn-dev/vgkeep/services/collection/migrations"
)

// One Postgres container serves this whole package. The per-test
// containers this replaces spent most of the package's runtime on
// boots, and that churn was the bulk of the Docker-daemon load behind
// the WSL2 connection-refused flakes. Each test still gets exactly
// what the old fixture gave it - a freshly migrated database and its
// own pool - via the drop-schema + re-migrate reset in newTestStore.
// No Terminate: the testcontainers reaper collects the container when
// the test process exits.
var sharedPG struct {
	once sync.Once
	url  string
	err  error
}

// newTestStore duplicates the fixture in migrations/migrations_test.go
// (Go test packages can't share helpers across packages).
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	sharedPG.once.Do(func() {
		pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
			tcpostgres.WithDatabase("collection"), tcpostgres.WithUsername("c"), tcpostgres.WithPassword("p"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).WithStartupTimeout(60*time.Second),
				wait.ForListeningPort("5432/tcp")))
		if err != nil {
			sharedPG.err = err
			return
		}
		sharedPG.url, sharedPG.err = pg.ConnectionString(ctx, "sslmode=disable")
	})
	if sharedPG.err != nil {
		t.Fatal(sharedPG.err)
	}
	// Reset: drop everything the previous test left (schema_migrations
	// included) and re-run the embedded migrations, so each test opens
	// on a fresh, fully migrated database - migration-seeded rows and
	// all. Two Execs because pgx's extended protocol takes one
	// statement at a time.
	conn, err := pgx.Connect(ctx, sharedPG.url)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			_ = conn.Close(ctx)
			t.Fatal(err)
		}
	}
	_ = conn.Close(ctx)
	if err := pgkit.Migrate(sharedPG.url, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgkit.Connect(ctx, sharedPG.url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return store.New(pool)
}

// baseEntry is a minimal valid game entry for userID; tests override
// the fields under test.
func baseEntry(userID uuid.UUID) store.Entry {
	return store.Entry{
		UserID:      userID,
		ProductID:   new(uuid.New()),
		ItemType:    "game",
		MediaType:   "physical",
		DisplayName: "Chrono Trigger",
		Region:      "ntsc_u",
		Packaging:   "cib",
		Currency:    "USD",
		PricingMode: "auto",
		Status:      "backlog",
		Source:      "manual",
	}
}

// customEntry is a minimal valid off-catalog entry.
func customEntry(userID uuid.UUID) store.Entry {
	e := baseEntry(userID)
	e.ProductID = nil
	e.DisplayName = "Chrono Trigger (fan translation cart)"
	e.PlatformName = new("SNES")
	e.PricingMode = "disabled"
	return e
}

func mustCreate(t *testing.T, s *store.Store, e store.Entry, tagIDs []uuid.UUID) store.Entry {
	t.Helper()
	created, err := s.CreateEntry(context.Background(), e, tagIDs)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return created
}

func rankOf(t *testing.T, e store.Entry) string {
	t.Helper()
	if e.BacklogRank == nil {
		t.Fatalf("entry %s has no backlog rank", e.ID)
	}
	return *e.BacklogRank
}

func TestCreateAssignsRankAtEndOfBacklog(t *testing.T) {
	s := newTestStore(t)
	user := uuid.New()
	a := mustCreate(t, s, baseEntry(user), nil)
	b := mustCreate(t, s, baseEntry(user), nil)
	c := mustCreate(t, s, baseEntry(user), nil)
	if rankOf(t, a) >= rankOf(t, b) || rankOf(t, b) >= rankOf(t, c) {
		t.Fatalf("ranks must append in order: %q %q %q", rankOf(t, a), rankOf(t, b), rankOf(t, c))
	}
	shelved := baseEntry(user)
	shelved.Status = "shelved"
	d := mustCreate(t, s, shelved, nil)
	if d.BacklogRank != nil {
		t.Fatal("a non-backlog create must not carry a rank")
	}
}

func TestCreateLinksOwnTagsOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, stranger := uuid.New(), uuid.New()
	own, err := s.CreateTag(ctx, user, "rpg")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateTag(ctx, stranger, "theirs")
	if err != nil {
		t.Fatal(err)
	}

	created := mustCreate(t, s, baseEntry(user), []uuid.UUID{own.ID, own.ID}) // duplicate ids collapse
	if len(created.Tags) != 1 || created.Tags[0].Name != "rpg" {
		t.Fatalf("tags: %+v", created.Tags)
	}

	if _, err := s.CreateEntry(ctx, baseEntry(user), []uuid.UUID{foreign.ID}); !errors.Is(err, store.ErrTagNotFound) {
		t.Fatalf("a foreign tag id must be ErrTagNotFound, got %v", err)
	}
	if _, err := s.CreateEntry(ctx, baseEntry(user), []uuid.UUID{uuid.New()}); !errors.Is(err, store.ErrTagNotFound) {
		t.Fatalf("an unknown tag id must be ErrTagNotFound, got %v", err)
	}
}

func TestCustomEntryLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	created := mustCreate(t, s, customEntry(user), nil)
	if created.ProductID != nil || created.PlatformName == nil || *created.PlatformName != "SNES" ||
		created.PlatformIGDBID != nil || created.IGDBGameID != nil {
		t.Fatalf("custom shape: %+v", created)
	}
	rankOf(t, created) // backlog customs rank like any entry

	// The user-owned display fields replace on update, and a proxy
	// carries the recommendation identity (igdb_game_id) with it.
	released := time.Date(1995, time.September, 30, 0, 0, 0, 0, time.UTC)
	created.DisplayName = "Chrono Trigger (repro cart, v2 patch)"
	created.PlatformName = new("Super Famicom")
	created.FirstReleaseDate = &released
	created.PricingMode = "proxy"
	created.PricingProductID = new(uuid.New())
	created.IGDBGameID = new(int64(1000))
	updated, err := s.UpdateEntry(ctx, created, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Chrono Trigger (repro cart, v2 patch)" ||
		*updated.PlatformName != "Super Famicom" || !updated.FirstReleaseDate.Equal(released) ||
		updated.IGDBGameID == nil || *updated.IGDBGameID != 1000 {
		t.Fatalf("custom display update: %+v", updated)
	}
}

func TestGetIsOwnerScoped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, stranger := uuid.New(), uuid.New()
	created := mustCreate(t, s, baseEntry(user), nil)

	got, err := s.GetEntry(ctx, user, created.ID)
	if err != nil || got.DisplayName != "Chrono Trigger" {
		t.Fatalf("owner read: %+v %v", got, err)
	}
	if _, err := s.GetEntry(ctx, stranger, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a foreign read must be ErrNotFound, got %v", err)
	}
}

func TestEntryCoverURLPersistsThroughCreateAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	cover := "https://images.igdb.example/chrono.jpg"

	e := baseEntry(userID)
	e.CoverURL = &cover
	e.CustomValueCents = new(int64(5400))
	e.CustomValueEnteredCents = new(int64(6000))
	e.CustomValueEnteredCurrency = new("EUR")
	created := mustCreate(t, s, e, nil)
	if created.CoverURL == nil || *created.CoverURL != cover {
		t.Fatalf("create must return the cover snapshot, got %v", created.CoverURL)
	}
	if created.CustomValueEnteredCents == nil || *created.CustomValueEnteredCents != 6000 ||
		created.CustomValueEnteredCurrency == nil || *created.CustomValueEnteredCurrency != "EUR" {
		t.Fatalf("create must return the entered pair, got %v %v",
			created.CustomValueEnteredCents, created.CustomValueEnteredCurrency)
	}
	got, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CoverURL == nil || *got.CoverURL != cover {
		t.Fatalf("read back: %v", got.CoverURL)
	}
	if got.CustomValueEnteredCents == nil || *got.CustomValueEnteredCents != 6000 ||
		got.CustomValueEnteredCurrency == nil || *got.CustomValueEnteredCurrency != "EUR" {
		t.Fatalf("read back entered pair: %v %v", got.CustomValueEnteredCents, got.CustomValueEnteredCurrency)
	}

	// Clearing the entered pair on update comes back nil.
	got.CustomValueEnteredCents = nil
	got.CustomValueEnteredCurrency = nil
	updated, err := s.UpdateEntry(ctx, got, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CustomValueEnteredCents != nil || updated.CustomValueEnteredCurrency != nil {
		t.Fatalf("update must clear the entered pair, got %v %v",
			updated.CustomValueEnteredCents, updated.CustomValueEnteredCurrency)
	}

	// Null stays null (customs and hardware never set one).
	bare := mustCreate(t, s, baseEntry(userID), nil)
	if bare.CoverURL != nil {
		t.Fatalf("no snapshot means null, got %v", *bare.CoverURL)
	}
}

// TestEntryLocalizedFieldsPersistThroughCreateAndUpdate covers the
// region-picked presentation trio: it round-trips through create and
// read back, an update rewrites it (a region edit re-picks, including
// back to nothing), and an entry whose region has no localized
// presentation stores NULLs.
func TestEntryLocalizedFieldsPersistThroughCreateAndUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()

	e := baseEntry(userID)
	e.Region = "ntsc_j"
	e.LocalizedName = new("聖剣伝説3")
	e.LocalizedNameTranslit = new("Seiken Densetsu 3")
	e.LocalizedCoverURL = new("https://images.igdb.example/jp.jpg")
	created := mustCreate(t, s, e, nil)
	if created.LocalizedName == nil || *created.LocalizedName != "聖剣伝説3" ||
		created.LocalizedNameTranslit == nil || *created.LocalizedNameTranslit != "Seiken Densetsu 3" ||
		created.LocalizedCoverURL == nil || *created.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
		t.Fatalf("create must return the localized snapshot: %v %v %v",
			created.LocalizedName, created.LocalizedNameTranslit, created.LocalizedCoverURL)
	}
	got, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LocalizedName == nil || *got.LocalizedName != "聖剣伝説3" ||
		got.LocalizedNameTranslit == nil || *got.LocalizedNameTranslit != "Seiken Densetsu 3" ||
		got.LocalizedCoverURL == nil || *got.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
		t.Fatalf("read back: %v %v %v", got.LocalizedName, got.LocalizedNameTranslit, got.LocalizedCoverURL)
	}

	// A region edit re-picks: a sparse bundle (cover only) overwrites
	// the whole trio rather than leaving the previous region's title
	// behind.
	got.Region = "pal"
	got.LocalizedName = nil
	got.LocalizedNameTranslit = nil
	got.LocalizedCoverURL = new("https://images.igdb.example/eu.jpg")
	updated, err := s.UpdateEntry(ctx, got, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LocalizedName != nil || updated.LocalizedNameTranslit != nil ||
		updated.LocalizedCoverURL == nil || *updated.LocalizedCoverURL != "https://images.igdb.example/eu.jpg" {
		t.Fatalf("update must rewrite the whole trio: %v %v %v",
			updated.LocalizedName, updated.LocalizedNameTranslit, updated.LocalizedCoverURL)
	}

	// No localized presentation means NULLs (the ntsc_u and
	// region_free case, and every hardware entry).
	bare := mustCreate(t, s, baseEntry(userID), nil)
	if bare.LocalizedName != nil || bare.LocalizedNameTranslit != nil || bare.LocalizedCoverURL != nil {
		t.Fatalf("no localized snapshot means nulls: %v %v %v",
			bare.LocalizedName, bare.LocalizedNameTranslit, bare.LocalizedCoverURL)
	}
}

func TestUpdateRankLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	a := mustCreate(t, s, baseEntry(user), nil)
	b := mustCreate(t, s, baseEntry(user), nil)
	origARank := rankOf(t, a)

	// Staying in backlog keeps the position.
	a.Notes = new("still first")
	kept, err := s.UpdateEntry(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rankOf(t, kept) != origARank {
		t.Fatalf("staying in backlog must keep the rank: %q -> %q", origARank, rankOf(t, kept))
	}

	// Leaving clears.
	a.Status = "beaten"
	left, err := s.UpdateEntry(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if left.BacklogRank != nil {
		t.Fatal("leaving the backlog must clear the rank")
	}

	// Re-entering appends at the END (after b), not at the old position.
	a.Status = "backlog"
	back, err := s.UpdateEntry(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rankOf(t, back) <= rankOf(t, b) {
		t.Fatalf("re-entering must append at the end: %q vs %q", rankOf(t, back), rankOf(t, b))
	}
}

func TestUpdateReplacesTagsAndIsScoped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	rpg, _ := s.CreateTag(ctx, user, "rpg")
	fav, _ := s.CreateTag(ctx, user, "favorites")
	created := mustCreate(t, s, baseEntry(user), []uuid.UUID{rpg.ID})

	created.Status = "backlog"
	updated, err := s.UpdateEntry(ctx, created, []uuid.UUID{fav.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Tags) != 1 || updated.Tags[0].Name != "favorites" {
		t.Fatalf("tag replace: %+v", updated.Tags)
	}

	foreign := created
	foreign.UserID = uuid.New()
	if _, err := s.UpdateEntry(ctx, foreign, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a foreign update must be ErrNotFound, got %v", err)
	}
}

// TestUpdateEntry_PersistsProductRepoint guards the narrow re-match:
// the UPDATE must write product_id, both in the row it returns and on
// a fresh reload (the RETURNING clause and the stored column must
// agree).
func TestUpdateEntry_PersistsProductRepoint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	e := mustCreate(t, s, baseEntry(userID), nil)

	newProduct := uuid.New()
	e.ProductID = &newProduct
	updated, err := s.UpdateEntry(ctx, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ProductID == nil || *updated.ProductID != newProduct {
		t.Fatalf("product_id must persist: %+v", updated.ProductID)
	}
	got, err := s.GetEntry(ctx, userID, e.ID)
	if err != nil || got.ProductID == nil || *got.ProductID != newProduct {
		t.Fatalf("reload: %+v, %v", got, err)
	}
}

// TestListGameBackedRefs seeds a product-backed game, a product-backed
// hardware entry, and a custom entry for TWO different users, plus a
// second game-backed entry sharing userA's product_id (a second copy
// of the same cart, different region), then asserts the resnapshot
// walk sees exactly the game-backed rows from BOTH users (the method
// is deliberately unscoped) and nothing else, ordered product_id then
// id (the shared product_id pair exercises the id tie-break), with
// each row's own region and stored localized trio read back correctly
// (gameA carries a non-nil trio, the other two rows carry nil - both
// round trips get exercised).
func TestListGameBackedRefs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()

	// seedTrio mirrors the brief's (a)/(b)/(c) shapes for one user and
	// returns the (a) game entry so the assertions below can key off it.
	// name/translit/cover seed the (a) entry's stored localized trio
	// (nil for callers that don't care), exercising both the non-nil
	// and nil round trip through ListGameBackedRefs.
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

	// A second game-backed entry on gameA's SAME product_id (a second
	// copy of the same cart), with a different region (pal). This gives
	// two rows an identical product_id, so the id tie-break in the
	// ORDER BY actually gets exercised, and it doubles as coverage that
	// per-entry regions come back correctly rather than just per-product.
	gameA2 := baseEntry(userA)
	gameA2.ProductID = gameA.ProductID
	gameA2.Region = "pal"
	gameA2.IGDBGameID = new(int64(1000))
	gameA2.FirstReleaseDate = &dateA2
	gameA2Entry := mustCreate(t, s, gameA2, nil)

	refs, err := s.ListGameBackedRefs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 {
		t.Fatalf("expected exactly the 3 game-backed rows (hardware/custom excluded), got %d: %+v", len(refs), refs)
	}

	// Ordering: product_id then id, checked across every consecutive
	// pair so the gameA/gameA2 pair (same product_id) actually exercises
	// the id tie-break instead of only the product_id sort. Postgres
	// orders uuid by raw bytes, which agrees with the canonical
	// hyphenated string's byte order (the hyphens sit at the same fixed
	// positions in every UUID), so comparing the String() form is a
	// faithful proxy for SQL order.
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
	want := map[uuid.UUID]struct {
		productID             uuid.UUID
		region                string
		release               time.Time
		name, translit, cover *string
	}{
		gameA.ID:       {*gameA.ProductID, "ntsc_u", dateA, jaName, jaTranslit, jaCover},
		gameA2Entry.ID: {*gameA.ProductID, "pal", dateA2, nil, nil, nil},
		gameB.ID:       {*gameB.ProductID, "ntsc_u", dateB, nil, nil, nil},
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
	}
}

// TestSetSnapshotFields covers the resnapshot walk's only write: it
// rewrites the date and the localized presentation trio in one UPDATE
// and bumps updated_at, and all-nil arguments clear every column back
// to NULL.
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
	if err := s.SetSnapshotFields(ctx, created.ID, &newDate, name, translit, cover); err != nil {
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
	if !got.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at must move: %v -> %v", created.UpdatedAt, got.UpdatedAt)
	}

	if err := s.SetSnapshotFields(ctx, created.ID, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.GetEntry(ctx, user, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.FirstReleaseDate != nil || cleared.LocalizedName != nil ||
		cleared.LocalizedNameTranslit != nil || cleared.LocalizedCoverURL != nil {
		t.Fatalf("all four columns must clear to NULL, got %+v", cleared)
	}
}

func TestDeleteEntry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	created := mustCreate(t, s, baseEntry(user), nil)

	if err := s.DeleteEntry(ctx, uuid.New(), created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a foreign delete must be ErrNotFound, got %v", err)
	}
	if err := s.DeleteEntry(ctx, user, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetEntry(ctx, user, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("deleted entry must be gone")
	}
	if err := s.DeleteEntry(ctx, user, created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("double delete must be ErrNotFound")
	}
}

func TestReorder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	a := mustCreate(t, s, baseEntry(user), nil)
	b := mustCreate(t, s, baseEntry(user), nil)
	c := mustCreate(t, s, baseEntry(user), nil)

	// Drop c into the a..b gap: order becomes a, c, b.
	moved, err := s.Reorder(ctx, user, c.ID, &a.ID, &b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rankOf(t, a) >= rankOf(t, moved) || rankOf(t, moved) >= rankOf(t, b) {
		t.Fatalf("c must land between a and b: %q %q %q", rankOf(t, a), rankOf(t, moved), rankOf(t, b))
	}

	// Move a to the very end (after b, nothing follows).
	endA, err := s.Reorder(ctx, user, a.ID, &b.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rankOf(t, endA) <= rankOf(t, b) {
		t.Fatal("a must land after b")
	}

	// Move b to the very start (nothing precedes, c follows).
	startB, err := s.Reorder(ctx, user, b.ID, nil, &moved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rankOf(t, startB) >= rankOf(t, moved) {
		t.Fatal("b must land before c")
	}

	// Reversed neighbors (a stale drag): endA now sorts after moved.
	if _, err := s.Reorder(ctx, user, startB.ID, &endA.ID, &moved.ID); !errors.Is(err, store.ErrConflictingOrder) {
		t.Fatalf("non-straddling neighbors must be ErrConflictingOrder, got %v", err)
	}

	// A non-backlog entry cannot reorder...
	shelved := baseEntry(user)
	shelved.Status = "shelved"
	d := mustCreate(t, s, shelved, nil)
	if _, err := s.Reorder(ctx, user, d.ID, nil, nil); !errors.Is(err, store.ErrNotInBacklog) {
		t.Fatalf("non-backlog reorder must be ErrNotInBacklog, got %v", err)
	}
	// ...and neither can a drag against a non-backlog neighbor.
	if _, err := s.Reorder(ctx, user, moved.ID, &d.ID, nil); !errors.Is(err, store.ErrNotInBacklog) {
		t.Fatalf("non-backlog neighbor must be ErrNotInBacklog, got %v", err)
	}

	// Unknown entry, and foreign scope.
	if _, err := s.Reorder(ctx, user, uuid.New(), nil, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown entry must be ErrNotFound, got %v", err)
	}
	if _, err := s.Reorder(ctx, uuid.New(), moved.ID, nil, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign reorder must be ErrNotFound, got %v", err)
	}
}

// paramsEqual compares two JSON byte slices for semantic equality.
func paramsEqual(t *testing.T, got, want []byte) bool {
	t.Helper()
	var gm, wm map[string]any
	if err := json.Unmarshal(got, &gm); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal(want, &wm); err != nil {
		t.Fatalf("unmarshal want: %v", err)
	}
	return reflect.DeepEqual(gm, wm)
}

// seedMatrix creates a deliberately varied collection for one user:
//
//	chrono  game      SNES(6) backlog  cib    ntsc_u  rating 9  paid 5000 USD  tags rpg,fav  year 1995  condition mint     purchased 2020-01-15
//	alundra game      PS1(7)  playing  loose  pal     no rating no paid        tags rpg      year 1997  no condition       purchased 2021-06-01
//	snes    console   SNES(6) shelved  cib    ntsc_u  no rating paid 12000 USD no tags       no year    no condition       purchased 2019-03-10
//	terra   game      SNES(6) backlog  sealed ntsc_j  rating 3  no paid        tags fav      year 1996  condition good     no purchase date  PINNED
//	pad     accessory (none)  shelved  loose  region_free       paid 2000 EUR  no tags       no year    no condition       no purchase date
func seedMatrix(t *testing.T, s *store.Store) (user uuid.UUID, byName map[string]store.Entry, tagIDs map[string]uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	user = uuid.New()
	rpg, err := s.CreateTag(ctx, user, "rpg")
	if err != nil {
		t.Fatal(err)
	}
	fav, err := s.CreateTag(ctx, user, "fav")
	if err != nil {
		t.Fatal(err)
	}
	tagIDs = map[string]uuid.UUID{"rpg": rpg.ID, "fav": fav.ID}

	mk := func(name string, mut func(*store.Entry), tags ...uuid.UUID) store.Entry {
		e := baseEntry(user)
		e.DisplayName = name
		mut(&e)
		return mustCreate(t, s, e, tags)
	}
	byName = map[string]store.Entry{}
	byName["chrono"] = mk("Chrono Trigger", func(e *store.Entry) {
		e.PlatformIGDBID, e.PlatformName = new(int64(6)), new("SNES")
		e.IGDBGameID = new(int64(1000))
		e.FirstReleaseDate = new(time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC))
		e.Rating, e.PricePaidCents = new(9), new(int64(5000))
		e.ItemCondition = new("mint")
		e.PurchasedAt = new(time.Date(2020, time.January, 15, 0, 0, 0, 0, time.UTC))
	}, rpg.ID, fav.ID)
	byName["alundra"] = mk("Alundra", func(e *store.Entry) {
		e.PlatformIGDBID, e.PlatformName = new(int64(7)), new("PS1")
		e.IGDBGameID = new(int64(1001))
		e.FirstReleaseDate = new(time.Date(1997, time.April, 11, 0, 0, 0, 0, time.UTC))
		e.Status, e.Packaging, e.Region = "playing", "loose", "pal"
		e.PurchasedAt = new(time.Date(2021, time.June, 1, 0, 0, 0, 0, time.UTC))
	}, rpg.ID)
	byName["snes"] = mk("Super Nintendo", func(e *store.Entry) {
		e.ItemType = "console"
		e.PlatformIGDBID, e.PlatformName = new(int64(6)), new("SNES")
		e.Status, e.PricePaidCents = "shelved", new(int64(12000))
		e.PurchasedAt = new(time.Date(2019, time.March, 10, 0, 0, 0, 0, time.UTC))
	})
	byName["terra"] = mk("Terranigma", func(e *store.Entry) {
		e.PlatformIGDBID, e.PlatformName = new(int64(6)), new("SNES")
		e.IGDBGameID = new(int64(1002))
		e.FirstReleaseDate = new(time.Date(1996, time.October, 19, 0, 0, 0, 0, time.UTC))
		e.Packaging, e.Region = "sealed", "ntsc_j"
		e.Rating, e.Pinned = new(3), true
		e.ItemCondition = new("good")
	}, fav.ID)
	byName["pad"] = mk("Controller", func(e *store.Entry) {
		e.ItemType = "accessory"
		e.Status, e.Packaging, e.Region = "shelved", "loose", "region_free"
		e.PricePaidCents, e.Currency = new(int64(2000)), "EUR"
	})
	return user, byName, tagIDs
}

func names(entries []store.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.DisplayName
	}
	return out
}

func wantNames(t *testing.T, got []store.Entry, want ...string) {
	t.Helper()
	g := names(got)
	if len(g) != len(want) {
		t.Fatalf("got %v, want %v", g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("got %v, want %v", g, want)
		}
	}
}

func TestListFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, _, tags := seedMatrix(t, s)
	list := func(f store.Filters) []store.Entry {
		t.Helper()
		got, err := s.ListEntries(ctx, user, f)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	base := store.Filters{Sort: "name", Order: "asc"}

	f := base
	f.ItemTypes = []string{"game"}
	wantNames(t, list(f), "Terranigma", "Alundra", "Chrono Trigger") // terra is pinned

	f = base
	f.Statuses = []string{"backlog", "playing"} // OR within a dimension
	wantNames(t, list(f), "Terranigma", "Alundra", "Chrono Trigger")

	f = base
	f.Packagings = []string{"cib"}
	wantNames(t, list(f), "Chrono Trigger", "Super Nintendo")

	f = base
	f.Regions = []string{"ntsc_u"}
	wantNames(t, list(f), "Chrono Trigger", "Super Nintendo")

	f = base
	f.PlatformIDs = []int64{6}
	wantNames(t, list(f), "Terranigma", "Chrono Trigger", "Super Nintendo")

	f = base
	f.ItemTypes, f.PlatformIDs = []string{"game"}, []int64{6} // AND across dimensions
	wantNames(t, list(f), "Terranigma", "Chrono Trigger")

	f = base
	f.TagIDs = []uuid.UUID{tags["rpg"]}
	wantNames(t, list(f), "Alundra", "Chrono Trigger")

	f = base
	f.TagIDs = []uuid.UUID{tags["rpg"], tags["fav"]} // ALL listed tags
	wantNames(t, list(f), "Chrono Trigger")

	f = base
	f.ItemConditions = []string{"mint"}
	wantNames(t, list(f), "Chrono Trigger")

	f = base
	f.ItemConditions = []string{"mint", "good"}           // OR within a dimension
	wantNames(t, list(f), "Terranigma", "Chrono Trigger") // terra is pinned

	f = base
	f.Statuses = []string{"dropped"}
	if got := list(f); len(got) != 0 {
		t.Fatalf("expected empty, got %v", names(got))
	}
}

func TestListSorts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, byName, _ := seedMatrix(t, s)
	list := func(f store.Filters) []store.Entry {
		t.Helper()
		got, err := s.ListEntries(ctx, user, f)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// Pinned prefixes the chosen sort; ties and nulls behave.
	wantNames(t, list(store.Filters{Sort: "name", Order: "asc"}),
		"Terranigma", "Alundra", "Chrono Trigger", "Controller", "Super Nintendo")
	wantNames(t, list(store.Filters{Sort: "rating", Order: "desc"}),
		"Terranigma", "Chrono Trigger", "Controller", "Super Nintendo", "Alundra") // nulls last, created_at DESC tiebreak
	wantNames(t, list(store.Filters{Sort: "paid", Order: "asc"}),
		"Terranigma", "Controller", "Chrono Trigger", "Super Nintendo", "Alundra")
	wantNames(t, list(store.Filters{Sort: "release_date", Order: "asc"}),
		"Terranigma", "Chrono Trigger", "Alundra", "Controller", "Super Nintendo")
	// purchased_at: snes (2019) < chrono (2020) < alundra (2021); terra
	// and pad never purchased (nulls last regardless of direction).
	wantNames(t, list(store.Filters{Sort: "purchased_at", Order: "asc"}),
		"Terranigma", "Super Nintendo", "Chrono Trigger", "Alundra", "Controller")
	wantNames(t, list(store.Filters{Sort: "purchased_at", Order: "desc"}),
		"Terranigma", "Alundra", "Chrono Trigger", "Super Nintendo", "Controller")
	wantNames(t, list(store.Filters{Sort: "created_at", Order: "asc"}),
		"Terranigma", "Chrono Trigger", "Alundra", "Super Nintendo", "Controller")

	// backlog_rank is the one sort WITHOUT the pinned prefix: pure
	// rank order (chrono was created before terra).
	f := store.Filters{Sort: "backlog_rank", Order: "asc"}
	f.Statuses = []string{"backlog"}
	wantNames(t, list(f), "Chrono Trigger", "Terranigma")

	// The value sort falls back to the stable base order here (the
	// handler re-sorts after price composition).
	wantNames(t, list(store.Filters{Sort: "value", Order: "asc"}),
		"Terranigma", "Controller", "Super Nintendo", "Alundra", "Chrono Trigger")

	// Tags ride along in bulk.
	got := list(store.Filters{Sort: "name", Order: "asc"})
	for _, e := range got {
		if e.DisplayName == byName["chrono"].DisplayName && len(e.Tags) != 2 {
			t.Fatalf("chrono must carry both tags, got %+v", e.Tags)
		}
		if e.Tags == nil {
			t.Fatal("tags must never be nil")
		}
	}
}

func TestLibrarySummary(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	// Two copies of game 2000: one dropped rating 5, one playing rating 8.
	c1 := baseEntry(user)
	c1.IGDBGameID, c1.Status, c1.Rating = new(int64(2000)), "dropped", new(5)
	mustCreate(t, s, c1, nil)
	c2 := baseEntry(user)
	c2.IGDBGameID, c2.Status, c2.Rating = new(int64(2000)), "playing", new(8)
	mustCreate(t, s, c2, nil)

	// One fully dropped game, unrated.
	d := baseEntry(user)
	d.IGDBGameID, d.Status = new(int64(2001)), "dropped"
	mustCreate(t, s, d, nil)

	// Hardware and id-less games stay out.
	h := baseEntry(user)
	h.ItemType, h.Status = "console", "shelved"
	mustCreate(t, s, h, nil)
	mustCreate(t, s, baseEntry(user), nil)

	lib, err := s.LibrarySummary(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib) != 2 {
		t.Fatalf("library: %+v", lib)
	}
	if lib[0].IGDBGameID != 2000 || lib[0].Rating == nil || *lib[0].Rating != 8 || lib[0].AllDropped {
		t.Fatalf("game 2000: %+v (best rating wins; not all copies dropped)", lib[0])
	}
	if lib[1].IGDBGameID != 2001 || lib[1].Rating != nil || !lib[1].AllDropped {
		t.Fatalf("game 2001: %+v (unrated, fully dropped)", lib[1])
	}
}

func TestTagsCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, stranger := uuid.New(), uuid.New()

	rpg, err := s.CreateTag(ctx, user, "RPG")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(ctx, user, "rpg"); !errors.Is(err, store.ErrNameTaken) {
		t.Fatalf("case-insensitive duplicate must be ErrNameTaken, got %v", err)
	}
	if _, err := s.CreateTag(ctx, stranger, "rpg"); err != nil {
		t.Fatalf("the same name under another user is fine: %v", err)
	}

	mustCreate(t, s, baseEntry(user), []uuid.UUID{rpg.ID})
	tags, err := s.ListTags(ctx, user)
	if err != nil || len(tags) != 1 || tags[0].Name != "RPG" || tags[0].EntryCount != 1 {
		t.Fatalf("list: %+v %v", tags, err)
	}

	if _, err := s.CreateTag(ctx, user, "later"); err != nil {
		t.Fatal(err)
	}
	renamed, err := s.RenameTag(ctx, user, rpg.ID, "role-playing")
	if err != nil || renamed.Name != "role-playing" || renamed.EntryCount != 1 {
		t.Fatalf("rename: %+v %v", renamed, err)
	}
	if _, err := s.RenameTag(ctx, user, rpg.ID, "LATER"); !errors.Is(err, store.ErrNameTaken) {
		t.Fatalf("rename onto a taken name must be ErrNameTaken, got %v", err)
	}
	if _, err := s.RenameTag(ctx, stranger, rpg.ID, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign rename must be ErrNotFound, got %v", err)
	}

	// Verify shared-tag and zero-tag entry_count via ListTags.
	second := baseEntry(user)
	second.DisplayName = "Final Fantasy"
	mustCreate(t, s, second, []uuid.UUID{rpg.ID})
	tags, err = s.ListTags(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 2 {
		t.Fatalf("after second entry: expected 2 tags, got %d", len(tags))
	}
	var rolePlayingTag, laterTag *store.Tag
	for i := range tags {
		if tags[i].Name == "role-playing" {
			rolePlayingTag = &tags[i]
		}
		if tags[i].Name == "later" {
			laterTag = &tags[i]
		}
	}
	if rolePlayingTag == nil {
		t.Fatalf("role-playing tag not found in %+v", tags)
	}
	if rolePlayingTag.EntryCount != 2 {
		t.Fatalf("role-playing entry_count: got %d, want 2", rolePlayingTag.EntryCount)
	}
	if laterTag == nil {
		t.Fatalf("later tag not found in %+v", tags)
	}
	if laterTag.EntryCount != 0 {
		t.Fatalf("later entry_count: got %d, want 0", laterTag.EntryCount)
	}

	if err := s.DeleteTag(ctx, user, rpg.ID); err != nil {
		t.Fatal(err)
	}
	entries, err := s.ListEntries(ctx, user, store.Filters{})
	if err != nil || len(entries) != 2 {
		t.Fatalf("after tag delete, expected 2 entries: %+v %v", entries, err)
	}
	// Both entries must have tags detached.
	for _, e := range entries {
		if len(e.Tags) != 0 {
			t.Fatalf("tag delete must detach from all entries: %+v", e.Tags)
		}
	}
	if err := s.DeleteTag(ctx, user, rpg.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double delete must be ErrNotFound, got %v", err)
	}
}

// TestCreateTag_PerUserCap pins the per-user distinct-tag ceiling: the
// 200th tag succeeds, the 201st answers ErrUserTagCapExceeded, and
// the count stops climbing (no half-committed row from the rejected
// attempt). A different user's own cap is untouched by this one.
func TestCreateTag_PerUserCap(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	for i := range store.TagCap {
		if _, err := s.CreateTag(ctx, user, fmt.Sprintf("tag-%03d", i)); err != nil {
			t.Fatalf("tag %d: %v", i, err)
		}
	}
	tags, err := s.ListTags(ctx, user)
	if err != nil || len(tags) != store.TagCap {
		t.Fatalf("expected exactly %d tags at the cap, got %d (%v)", store.TagCap, len(tags), err)
	}

	if _, err := s.CreateTag(ctx, user, "one-too-many"); !errors.Is(err, store.ErrUserTagCapExceeded) {
		t.Fatalf("the tag past the cap must be ErrUserTagCapExceeded, got %v", err)
	}
	tags, err = s.ListTags(ctx, user)
	if err != nil || len(tags) != store.TagCap {
		t.Fatalf("a rejected create must not land a row: got %d tags (%v)", len(tags), err)
	}

	if _, err := s.CreateTag(ctx, uuid.New(), "fresh user, fresh cap"); err != nil {
		t.Fatalf("a different user must not see this user's cap: %v", err)
	}
}

// TestBulkUpdateEntries_ScalarActionsAndOwnershipFiltering pins the
// flat scalar update (status + storage_location, including
// bulk-update's OWN clearing rule: an explicit empty string clears,
// an absent field leaves the column untouched - the opposite of the
// full-replacement update) plus the ownership-filtering posture: ids
// that are not the caller's own (foreign or unknown) are silently
// excluded from updated_count and never written.
func TestBulkUpdateEntries_ScalarActionsAndOwnershipFiltering(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, stranger := uuid.New(), uuid.New()

	mine := baseEntry(user)
	mine.StorageLocation = new("shelf A")
	created := mustCreate(t, s, mine, nil)
	theirs := mustCreate(t, s, baseEntry(stranger), nil)
	unknown := uuid.New()

	status, loc := "shelved", "closet B"
	count, err := s.BulkUpdateEntries(ctx, user,
		[]uuid.UUID{created.ID, theirs.ID, unknown},
		store.BulkActions{Status: &status, StorageLocation: &loc})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("updated_count must count only the caller's own entry among entry_ids, got %d", count)
	}

	got, err := s.GetEntry(ctx, user, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "shelved" || got.StorageLocation == nil || *got.StorageLocation != "closet B" {
		t.Fatalf("scalar update did not apply: %+v", got)
	}
	theirsAfter, err := s.GetEntry(ctx, stranger, theirs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if theirsAfter.Status == "shelved" {
		t.Fatal("a foreign entry must never be written")
	}

	// Empty string clears; a field absent from THIS call leaves the
	// column untouched (status stays "shelved" from the call above).
	empty := ""
	count, err = s.BulkUpdateEntries(ctx, user, []uuid.UUID{created.ID}, store.BulkActions{StorageLocation: &empty})
	if err != nil || count != 1 {
		t.Fatalf("clear: count=%d err=%v", count, err)
	}
	got, err = s.GetEntry(ctx, user, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageLocation != nil {
		t.Fatalf("empty string must clear storage_location, got %v", *got.StorageLocation)
	}
	if got.Status != "shelved" {
		t.Fatalf("a status action absent from this call must leave status untouched, got %q", got.Status)
	}
}

// TestBulkUpdateEntries_EnteringBacklogAppendsAndPreservesPosition
// pins the other half of backlog_rank management (the entries table
// CHECKs status='backlog' exactly when backlog_rank is set, so a bare
// status write cannot skip this): entries newly entering backlog get
// a fresh rank appended at the end, oldest-created first among the
// batch, while an entry already in backlog keeps its existing rank
// untouched.
func TestBulkUpdateEntries_EnteringBacklogAppendsAndPreservesPosition(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	already := mustCreate(t, s, baseEntry(user), nil)
	existingRank := rankOf(t, already)

	older := baseEntry(user)
	older.Status = "shelved"
	older.DisplayName = "older"
	oldEntry := mustCreate(t, s, older, nil)

	newer := baseEntry(user)
	newer.Status = "playing"
	newer.DisplayName = "newer"
	newEntry := mustCreate(t, s, newer, nil)

	status := "backlog"
	count, err := s.BulkUpdateEntries(ctx, user,
		[]uuid.UUID{already.ID, oldEntry.ID, newEntry.ID},
		store.BulkActions{Status: &status})
	if err != nil || count != 3 {
		t.Fatalf("count=%d err=%v", count, err)
	}

	gotAlready, err := s.GetEntry(ctx, user, already.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rankOf(t, gotAlready) != existingRank {
		t.Fatalf("an entry already in backlog must keep its rank: got %q, want %q", rankOf(t, gotAlready), existingRank)
	}

	gotOld, err := s.GetEntry(ctx, user, oldEntry.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotNew, err := s.GetEntry(ctx, user, newEntry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotOld.Status != "backlog" || gotNew.Status != "backlog" {
		t.Fatalf("both must now be backlog: old=%q new=%q", gotOld.Status, gotNew.Status)
	}
	if rankOf(t, gotOld) <= existingRank || rankOf(t, gotNew) <= existingRank {
		t.Fatalf("newly-entering entries must append AFTER the existing backlog rank: old=%q new=%q existing=%q",
			rankOf(t, gotOld), rankOf(t, gotNew), existingRank)
	}
	if rankOf(t, gotOld) >= rankOf(t, gotNew) {
		t.Fatalf("newly-entering entries must append oldest-created first: old=%q, new=%q", rankOf(t, gotOld), rankOf(t, gotNew))
	}
}

// TestBulkUpdateEntries_TagAddRemove pins the tag delta across a
// batch: add_tag_ids attaches to every targeted (owned) entry, a
// foreign tag id in add_tag_ids matches nothing (same ownership
// posture as replaceTags), and remove_tag_ids detaches.
func TestBulkUpdateEntries_TagAddRemove(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, stranger := uuid.New(), uuid.New()

	rpg, _ := s.CreateTag(ctx, user, "rpg")
	fav, _ := s.CreateTag(ctx, user, "favorites")
	foreign, _ := s.CreateTag(ctx, stranger, "theirs")

	a := mustCreate(t, s, baseEntry(user), nil)
	second := baseEntry(user)
	second.DisplayName = "second copy"
	b := mustCreate(t, s, second, nil)

	count, err := s.BulkUpdateEntries(ctx, user, []uuid.UUID{a.ID, b.ID},
		store.BulkActions{AddTagIDs: []uuid.UUID{rpg.ID, fav.ID, foreign.ID}})
	if err != nil || count != 2 {
		t.Fatalf("add: count=%d err=%v", count, err)
	}
	for _, id := range []uuid.UUID{a.ID, b.ID} {
		got, err := s.GetEntry(ctx, user, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Tags) != 2 {
			t.Fatalf("entry %s: expected rpg+favorites only (the foreign tag must no-op), got %+v", id, got.Tags)
		}
	}

	count, err = s.BulkUpdateEntries(ctx, user, []uuid.UUID{a.ID, b.ID},
		store.BulkActions{RemoveTagIDs: []uuid.UUID{rpg.ID}})
	if err != nil || count != 2 {
		t.Fatalf("remove: count=%d err=%v", count, err)
	}
	for _, id := range []uuid.UUID{a.ID, b.ID} {
		got, err := s.GetEntry(ctx, user, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Tags) != 1 || got.Tags[0].Name != "favorites" {
			t.Fatalf("entry %s: expected favorites only after remove, got %+v", id, got.Tags)
		}
	}
}

// TestBulkUpdateEntries_PerEntryTagCapRollsBackWholeTransaction pins
// the all-or-nothing contract: one entry crossing the 50-tag ceiling
// rolls back the ENTIRE transaction, including the scalar change and
// the tag adds this call would otherwise have made to an entry that,
// on its own, would have stayed under the cap.
func TestBulkUpdateEntries_PerEntryTagCapRollsBackWholeTransaction(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	// 53 distinct tags: 48 pre-attached to the first entry directly at
	// store level (bypasses the handler's per-call 50 max on
	// tag_ids), 5 new ones this call adds.
	all := make([]uuid.UUID, 53)
	for i := range all {
		tag, err := s.CreateTag(ctx, user, fmt.Sprintf("t%02d", i))
		if err != nil {
			t.Fatal(err)
		}
		all[i] = tag.ID
	}
	overloaded := baseEntry(user)
	overloaded.DisplayName = "overloaded"
	a := mustCreate(t, s, overloaded, all[:48])
	fresh := baseEntry(user)
	fresh.DisplayName = "fresh"
	b := mustCreate(t, s, fresh, nil)

	status := "playing"
	_, err := s.BulkUpdateEntries(ctx, user, []uuid.UUID{a.ID, b.ID},
		store.BulkActions{AddTagIDs: all[48:], Status: &status})
	if !errors.Is(err, store.ErrTagCapExceeded) {
		t.Fatalf("expected ErrTagCapExceeded, got %v", err)
	}

	gotA, err := s.GetEntry(ctx, user, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotA.Tags) != 48 || gotA.Status == "playing" {
		t.Fatalf("the whole transaction must roll back: entry a has %d tags, status %q", len(gotA.Tags), gotA.Status)
	}
	gotB, err := s.GetEntry(ctx, user, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotB.Tags) != 0 || gotB.Status == "playing" {
		t.Fatalf("a skipped-entry partial apply is forbidden: entry b has %d tags, status %q", len(gotB.Tags), gotB.Status)
	}
}

// TestBulkUpdateEntries_IdempotentReRun pins that re-running the same
// bulk-update reports the same updated_count even when every action
// was already true (the tag already attached, the fields already
// holding these values) - the repo's re-runnable-lever posture.
func TestBulkUpdateEntries_IdempotentReRun(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	tag, _ := s.CreateTag(ctx, user, "rpg")
	a := mustCreate(t, s, baseEntry(user), nil)
	b := mustCreate(t, s, baseEntry(user), nil)

	status := "shelved"
	actions := store.BulkActions{AddTagIDs: []uuid.UUID{tag.ID}, Status: &status}
	first, err := s.BulkUpdateEntries(ctx, user, []uuid.UUID{a.ID, b.ID}, actions)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.BulkUpdateEntries(ctx, user, []uuid.UUID{a.ID, b.ID}, actions)
	if err != nil {
		t.Fatal(err)
	}
	if first != 2 || second != 2 {
		t.Fatalf("idempotent re-run: first=%d second=%d, want 2 both times", first, second)
	}
}

func TestViewsCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, stranger := uuid.New(), uuid.New()
	params := []byte(`{"filters":{"status":["backlog"]},"sort":"rating","view_mode":"grid"}`)

	v, err := s.CreateView(ctx, user, "Backlog by rating", params, "private")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateView(ctx, user, "backlog BY RATING", params, "private"); !errors.Is(err, store.ErrNameTaken) {
		t.Fatalf("case-insensitive duplicate must be ErrNameTaken, got %v", err)
	}

	views, err := s.ListViews(ctx, user)
	if err != nil || len(views) != 1 || string(views[0].Params) == "" {
		t.Fatalf("list: %+v %v", views, err)
	}
	// Verify params round-trip semantically.
	if !paramsEqual(t, views[0].Params, params) {
		t.Fatalf("params round-trip: got %q, want %q", string(views[0].Params), string(params))
	}
	if got, _ := s.ListViews(ctx, stranger); len(got) != 0 {
		t.Fatal("views are user-scoped")
	}

	newParams := []byte(`{"view_mode":"table"}`)
	updated, err := s.UpdateView(ctx, user, v.ID, "Shelf", newParams, "private")
	if err != nil || updated.Name != "Shelf" {
		t.Fatalf("update: %+v %v", updated, err)
	}
	// Verify new params were stored, not old ones.
	if !paramsEqual(t, updated.Params, newParams) {
		t.Fatalf("params after update: got %q, want %q", string(updated.Params), string(newParams))
	}
	if updated.UpdatedAt.Before(v.UpdatedAt) {
		t.Fatal("updated_at must not regress")
	}
	if _, err := s.UpdateView(ctx, stranger, v.ID, "x", params, "private"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign update must be ErrNotFound, got %v", err)
	}

	if err := s.DeleteView(ctx, user, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteView(ctx, user, v.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double delete must be ErrNotFound, got %v", err)
	}
}

func TestViews_SlugAndVisibility(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	v1, err := s.CreateView(ctx, user, "SNES * Favorites", []byte(`{"v":1}`), "private")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if v1.Slug != "SNES_Favorites" || v1.Visibility != "private" || v1.PublishedAt != nil {
		t.Fatalf("v1 = %+v", v1)
	}

	// Distinct name folding to the same slug key: suffix dedupe.
	v2, err := s.CreateView(ctx, user, "snes favorites", []byte(`{"v":1}`), "private")
	if err != nil {
		t.Fatalf("create twin: %v", err)
	}
	if v2.Slug != "snes_favorites2" {
		t.Fatalf("deduped slug = %q", v2.Slug)
	}

	// Publish stamps published_at; re-saving while listed keeps it.
	pub, err := s.UpdateView(ctx, user, v1.ID, v1.Name, v1.Params, "listed")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if pub.PublishedAt == nil {
		t.Fatal("publish must stamp published_at")
	}
	again, err := s.UpdateView(ctx, user, v1.ID, v1.Name, v1.Params, "listed")
	if err != nil {
		t.Fatalf("re-save: %v", err)
	}
	if !again.PublishedAt.Equal(*pub.PublishedAt) {
		t.Fatal("re-save while listed must not re-stamp published_at")
	}

	// Unlist then re-list: fresh stamp.
	if _, err := s.UpdateView(ctx, user, v1.ID, v1.Name, v1.Params, "unlisted"); err != nil {
		t.Fatalf("unlist: %v", err)
	}
	relist, err := s.UpdateView(ctx, user, v1.ID, v1.Name, v1.Params, "listed")
	if err != nil {
		t.Fatalf("relist: %v", err)
	}
	if !relist.PublishedAt.After(*pub.PublishedAt) {
		t.Fatal("re-list must re-stamp published_at")
	}

	// Slug resolution folds; wrong owner misses.
	got, err := s.GetSharedShelfBySlug(ctx, user, store.NormalizeSlug("snes__FAVORITES"))
	if err != nil || got.ID != v1.ID {
		t.Fatalf("by-slug = %+v, %v", got, err)
	}
	if _, err := s.GetSharedShelfBySlug(ctx, uuid.New(), store.NormalizeSlug("snes_favorites")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign-owner slug err = %v", err)
	}
}

// TestUpdateView_UnchangedNameKeepsSlug guards the only-renames-break-
// links promise: "Games", "Games!", and "Games?" all derive the same
// base slug, so they dedupe to Games, Games2, and Games3. Deleting
// Games! frees Games2. A later params/visibility-only save of Games?
// must not re-derive and silently drop onto the now-free Games2 -
// that would move Games? out from under anyone who already has its
// Games3 link.
func TestUpdateView_UnchangedNameKeepsSlug(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	params := []byte(`{"v":1}`)

	if _, err := s.CreateView(ctx, user, "Games", params, "private"); err != nil {
		t.Fatalf("create Games: %v", err)
	}
	bang, err := s.CreateView(ctx, user, "Games!", params, "private")
	if err != nil {
		t.Fatalf("create Games!: %v", err)
	}
	if bang.Slug != "Games2" {
		t.Fatalf("Games! slug = %q, want Games2", bang.Slug)
	}
	huh, err := s.CreateView(ctx, user, "Games?", params, "private")
	if err != nil {
		t.Fatalf("create Games?: %v", err)
	}
	if huh.Slug != "Games3" {
		t.Fatalf("Games? slug = %q, want Games3", huh.Slug)
	}

	if err := s.DeleteView(ctx, user, bang.ID); err != nil {
		t.Fatalf("delete Games!: %v", err)
	}

	newParams := []byte(`{"v":2}`)
	updated, err := s.UpdateView(ctx, user, huh.ID, huh.Name, newParams, "unlisted")
	if err != nil {
		t.Fatalf("update Games? (same name, new params+visibility): %v", err)
	}
	if updated.Slug != "Games3" {
		t.Fatalf("slug after non-rename update = %q, want unchanged Games3", updated.Slug)
	}
	if !paramsEqual(t, updated.Params, newParams) {
		t.Fatalf("params after update: got %q, want %q", string(updated.Params), string(newParams))
	}
}

func TestSeedDefaultViews(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	if err := s.SeedDefaultViews(ctx, user); err != nil {
		t.Fatalf("seed: %v", err)
	}
	views, err := s.ListViews(ctx, user)
	if err != nil || len(views) != 2 {
		t.Fatalf("views = %d, %v", len(views), err)
	}
	// Name order: Backlog, Full collection.
	if views[0].Name != "Backlog" || views[0].Slug != "Backlog" {
		t.Fatalf("backlog = %+v", views[0])
	}
	if views[1].Name != "Full collection" || views[1].Slug != "Full_Collection" {
		t.Fatalf("full = %+v", views[1])
	}
	// Re-seed inserts nothing while both defaults still exist: the
	// ON CONFLICT DO NOTHING makes the second call a no-op, not a
	// duplicate pair.
	if err := s.SeedDefaultViews(ctx, user); err != nil {
		t.Fatalf("re-seed: %v", err)
	}
	if views, _ = s.ListViews(ctx, user); len(views) != 2 {
		t.Fatalf("re-seed duplicated: %d", len(views))
	}
}

func TestListListedShelves_And_ByIDs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()

	a1, _ := s.CreateView(ctx, alice, "Shelf One", []byte(`{"v":1}`), "listed")
	a2, _ := s.CreateView(ctx, alice, "Shelf Two", []byte(`{"v":1}`), "unlisted")
	b1, _ := s.CreateView(ctx, bob, "Bob Shelf", []byte(`{"v":1}`), "listed")

	// Only alice in the owner set: bob's listed shelf stays out.
	shelves, total, err := s.ListListedShelves(ctx, []uuid.UUID{alice}, 20, 0)
	if err != nil || total != 1 || len(shelves) != 1 || shelves[0].ID != a1.ID {
		t.Fatalf("listed = %+v total=%d err=%v", shelves, total, err)
	}

	// by-ids returns non-private (listed + unlisted), drops private.
	if _, err := s.UpdateView(ctx, bob, b1.ID, b1.Name, b1.Params, "private"); err != nil {
		t.Fatalf("privatize: %v", err)
	}
	got, err := s.SharedShelvesByIDs(ctx, []uuid.UUID{a1.ID, a2.ID, b1.ID})
	if err != nil || len(got) != 2 {
		t.Fatalf("by-ids = %+v, %v", got, err)
	}
}

// TestListListedShelves_OrderingAndPagination pins the two properties
// ListListedShelves promises beyond simple owner filtering: newest
// publish first (published_at DESC), and limit/offset windows that
// tile the full ordered set without gaps or overlap.
func TestListListedShelves_OrderingAndPagination(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	owner := uuid.New()

	// Staggered publish times: CreateView stamps published_at = now()
	// at insert, so a short sleep between creates guarantees a strict
	// order regardless of test-machine speed.
	first, err := s.CreateView(ctx, owner, "First", []byte(`{"v":1}`), "listed")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	second, err := s.CreateView(ctx, owner, "Second", []byte(`{"v":1}`), "listed")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	third, err := s.CreateView(ctx, owner, "Third", []byte(`{"v":1}`), "listed")
	if err != nil {
		t.Fatalf("create third: %v", err)
	}

	// Full page: newest publish first.
	all, total, err := s.ListListedShelves(ctx, []uuid.UUID{owner}, 20, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("all = %+v total=%d", all, total)
	}
	if all[0].ID != third.ID || all[1].ID != second.ID || all[2].ID != first.ID {
		t.Fatalf("order = [%s %s %s], want [Third Second First]", all[0].Name, all[1].Name, all[2].Name)
	}

	// Page windows tile the ordered set: limit=2 offset=0 is the two
	// newest; limit=2 offset=2 is the remaining oldest one; total_count
	// stays the full 3 on every page.
	page1, total1, err := s.ListListedShelves(ctx, []uuid.UUID{owner}, 2, 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if total1 != 3 || len(page1) != 2 || page1[0].ID != third.ID || page1[1].ID != second.ID {
		t.Fatalf("page1 = %+v total=%d", page1, total1)
	}
	page2, total2, err := s.ListListedShelves(ctx, []uuid.UUID{owner}, 2, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if total2 != 3 || len(page2) != 1 || page2[0].ID != first.ID {
		t.Fatalf("page2 = %+v total=%d", page2, total2)
	}
}

// TestListListedShelves_Unfiltered pins the owner_ids-absent
// contract (Explore-recent's read): a nil ownerIDs slice lists
// listed shelves across EVERY owner, still excludes unlisted and
// private rows, still orders newest-publish-first across owners,
// and still paginates - the same properties
// TestListListedShelves_OrderingAndPagination pins for the
// owner-filtered call, now proven for the unfiltered one.
func TestListListedShelves_Unfiltered(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()

	// Staggered publish times, same idiom as
	// TestListListedShelves_OrderingAndPagination: CreateView stamps
	// published_at = now() at insert, so a short sleep between
	// creates guarantees a strict cross-owner order.
	aFirst, err := s.CreateView(ctx, alice, "Alice First", []byte(`{"v":1}`), "listed")
	if err != nil {
		t.Fatalf("create alice first: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	bOnly, err := s.CreateView(ctx, bob, "Bob Only", []byte(`{"v":1}`), "listed")
	if err != nil {
		t.Fatalf("create bob only: %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	aSecond, err := s.CreateView(ctx, alice, "Alice Second", []byte(`{"v":1}`), "listed")
	if err != nil {
		t.Fatalf("create alice second: %v", err)
	}
	if _, err := s.CreateView(ctx, alice, "Alice Unlisted", []byte(`{"v":1}`), "unlisted"); err != nil {
		t.Fatalf("create alice unlisted: %v", err)
	}
	if _, err := s.CreateView(ctx, bob, "Bob Private", []byte(`{"v":1}`), "private"); err != nil {
		t.Fatalf("create bob private: %v", err)
	}

	// nil ownerIDs: every listed shelf across both owners, newest
	// publish first, unlisted and private excluded regardless of
	// owner.
	all, total, err := s.ListListedShelves(ctx, nil, 20, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("all = %+v total=%d, want the 3 listed rows across both owners", all, total)
	}
	if all[0].ID != aSecond.ID || all[1].ID != bOnly.ID || all[2].ID != aFirst.ID {
		t.Fatalf("order = [%s %s %s], want [Alice Second, Bob Only, Alice First]",
			all[0].Name, all[1].Name, all[2].Name)
	}

	// limit/offset still tile the unfiltered set: limit=2 offset=0 is
	// the two newest; limit=2 offset=2 is the remaining oldest one;
	// total_count stays the full 3 on every page.
	page1, total1, err := s.ListListedShelves(ctx, nil, 2, 0)
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if total1 != 3 || len(page1) != 2 || page1[0].ID != aSecond.ID || page1[1].ID != bOnly.ID {
		t.Fatalf("page1 = %+v total=%d", page1, total1)
	}
	page2, total2, err := s.ListListedShelves(ctx, nil, 2, 2)
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if total2 != 3 || len(page2) != 1 || page2[0].ID != aFirst.ID {
		t.Fatalf("page2 = %+v total=%d", page2, total2)
	}
}

func TestGetSharedShelf(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	params := []byte(`{"v":1,"sort":"name"}`)

	v, err := s.CreateView(ctx, user, "Arcade Cabinet", params, "unlisted")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetSharedShelf(ctx, v.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != v.ID || got.UserID != user || got.Name != v.Name ||
		got.Slug != v.Slug || got.Visibility != "unlisted" {
		t.Fatalf("got = %+v", got)
	}
	if !paramsEqual(t, got.Params, params) {
		t.Fatalf("params = %s", got.Params)
	}

	if _, err := s.GetSharedShelf(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown id = %v, want ErrNotFound", err)
	}
}

func TestCountEntriesFiltered(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, _, _ := seedMatrix(t, s)

	// Games only: chrono + alundra + terra; the console and the
	// platformless accessory are excluded.
	games, err := s.CountEntriesFiltered(ctx, user, store.Filters{ItemTypes: []string{"game"}})
	if err != nil {
		t.Fatal(err)
	}
	if games != 3 {
		t.Fatalf("games = %d, want 3", games)
	}

	all, err := s.CountEntriesFiltered(ctx, user, store.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if all != 5 {
		t.Fatalf("unfiltered = %d, want 5", all)
	}
}

func TestCoverURLs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	mustCreate(t, s, baseEntry(user), nil) // no cover: excluded
	withEmpty := baseEntry(user)
	withEmpty.CoverURL = new("")
	mustCreate(t, s, withEmpty, nil) // empty cover: excluded

	covers := []string{
		"https://images.igdb.example/cover-1.jpg",
		"https://images.igdb.example/cover-2.jpg",
		"https://images.igdb.example/cover-3.jpg",
		"https://images.igdb.example/cover-4.jpg",
	}
	for _, c := range covers {
		e := baseEntry(user)
		e.CoverURL = new(c)
		mustCreate(t, s, e, nil)
	}

	// Nil and empty covers never surface, however generous the limit.
	all, err := s.CoverURLs(ctx, user, store.Filters{}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(covers) {
		t.Fatalf("covered urls = %v, want %d entries", all, len(covers))
	}

	// The limit is honored, and the default order is newest-first.
	top2, err := s.CoverURLs(ctx, user, store.Filters{}, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{covers[3], covers[2]}
	if len(top2) != 2 || top2[0] != want[0] || top2[1] != want[1] {
		t.Fatalf("top 2 = %v, want %v", top2, want)
	}
}

func TestDashboardAggregates(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, _, _ := seedMatrix(t, s)

	// Create a stranger user with entries to verify isolation.
	stranger := uuid.New()
	strangerEntry := baseEntry(stranger)
	strangerEntry.Status = "playing"
	strangerEntry.PricePaidCents = new(int64(99999))
	mustCreate(t, s, strangerEntry, nil)
	mustCreate(t, s, baseEntry(stranger), nil)

	counts, err := s.DashboardCounts(ctx, user, store.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 5 {
		t.Fatalf("total: %d", counts.Total)
	}
	if counts.ByStatus["backlog"] != 2 || counts.ByStatus["playing"] != 1 || counts.ByStatus["shelved"] != 2 {
		t.Fatalf("by status: %+v", counts.ByStatus)
	}
	if counts.ByItemType["game"] != 3 || counts.ByItemType["console"] != 1 || counts.ByItemType["accessory"] != 1 {
		t.Fatalf("by item type: %+v", counts.ByItemType)
	}
	// SNES(3) first, then PS1(1) and the platformless accessory ("").
	if len(counts.ByPlatform) != 3 || counts.ByPlatform[0].Name != "SNES" || counts.ByPlatform[0].Count != 3 {
		t.Fatalf("by platform: %+v", counts.ByPlatform)
	}
	// chrono 5000 + snes 12000 USD; pad 2000 EUR.
	if len(counts.Spend) != 2 ||
		counts.Spend[0].Currency != "EUR" || counts.Spend[0].TotalCents != 2000 ||
		counts.Spend[1].Currency != "USD" || counts.Spend[1].TotalCents != 17000 {
		t.Fatalf("spend: %+v", counts.Spend)
	}

	prows, err := s.PricingRows(ctx, user, store.Filters{})
	if err != nil || len(prows) != 5 {
		t.Fatalf("pricing rows: %+v %v", prows, err)
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

func TestDashboardAggregatesFiltered(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, _, tagIDs := seedMatrix(t, s)

	// Games only: chrono + alundra + terra; the platformless accessory
	// and the console drop out of every aggregate.
	games, err := s.DashboardCounts(ctx, user, store.Filters{ItemTypes: []string{"game"}})
	if err != nil {
		t.Fatal(err)
	}
	if games.Total != 3 || games.ByStatus["backlog"] != 2 || games.ByStatus["playing"] != 1 {
		t.Fatalf("games: %+v", games)
	}
	if len(games.ByPlatform) != 2 || games.ByPlatform[0].Name != "SNES" || games.ByPlatform[0].Count != 2 {
		t.Fatalf("games by platform: %+v", games.ByPlatform)
	}
	// Only chrono records a price among the games.
	if len(games.Spend) != 1 || games.Spend[0].Currency != "USD" || games.Spend[0].TotalCents != 5000 {
		t.Fatalf("games spend: %+v", games.Spend)
	}

	// Dimensions AND together: backlog on SNES = chrono + terra.
	both, err := s.DashboardCounts(ctx, user, store.Filters{
		Statuses: []string{"backlog"}, PlatformIDs: []int64{6},
	})
	if err != nil || both.Total != 2 {
		t.Fatalf("backlog on SNES: %+v %v", both, err)
	}

	// tag_id requires ALL listed tags: rpg+fav = chrono alone.
	tagged, err := s.DashboardCounts(ctx, user, store.Filters{
		TagIDs: []uuid.UUID{tagIDs["rpg"], tagIDs["fav"]},
	})
	if err != nil || tagged.Total != 1 {
		t.Fatalf("rpg+fav: %+v %v", tagged, err)
	}

	prows, err := s.PricingRows(ctx, user, store.Filters{ItemTypes: []string{"game"}})
	if err != nil || len(prows) != 3 {
		t.Fatalf("filtered pricing rows: %+v %v", prows, err)
	}
}

func TestEntryCustomValueSetAtLifecycle(t *testing.T) {
	s := newTestStore(t)
	userID := uuid.New()
	v1, v2 := int64(9900), int64(12000)

	e := baseEntry(userID)
	e.PricingMode = "custom"
	e.CustomValueCents = &v1
	created, err := s.CreateEntry(context.Background(), e, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.CustomValueSetAt == nil {
		t.Fatal("create with a value must stamp set-at")
	}
	stamp := *created.CustomValueSetAt

	// Unchanged value keeps the stamp.
	kept, err := s.UpdateEntry(context.Background(), created, nil)
	if err != nil {
		t.Fatal(err)
	}
	if kept.CustomValueSetAt == nil || !kept.CustomValueSetAt.Equal(stamp) {
		t.Fatalf("unchanged value must keep set-at %v, got %v", stamp, kept.CustomValueSetAt)
	}

	// Mode toggle alone keeps value and stamp (memory).
	kept.PricingMode = "disabled"
	kept2, err := s.UpdateEntry(context.Background(), kept, nil)
	if err != nil {
		t.Fatal(err)
	}
	if kept2.CustomValueCents == nil || *kept2.CustomValueCents != v1 || !kept2.CustomValueSetAt.Equal(stamp) {
		t.Fatal("leaving custom mode must not touch the value pair")
	}

	// Changed value restamps.
	kept2.PricingMode = "custom"
	kept2.CustomValueCents = &v2
	restamped, err := s.UpdateEntry(context.Background(), kept2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !restamped.CustomValueSetAt.After(stamp) {
		t.Fatalf("changed value must restamp: %v !> %v", restamped.CustomValueSetAt, stamp)
	}

	// Clearing the value clears the stamp (pair CHECK).
	restamped.PricingMode = "disabled"
	restamped.CustomValueCents = nil
	cleared, err := s.UpdateEntry(context.Background(), restamped, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.CustomValueCents != nil || cleared.CustomValueSetAt != nil {
		t.Fatal("clearing the value must clear the pair")
	}
}

func TestCountEntriesByProduct(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	productID := uuid.New()

	// Two entries on the counted product across DIFFERENT users - the
	// count is catalog-wide, not caller-scoped - plus unrelated noise
	// (another product, a custom entry) that must not count.
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

func TestSubmissions_LifecycleCapsAndQueue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()

	custom, err := s.CreateEntry(ctx, customEntry(userID), nil)
	if err != nil {
		t.Fatal(err)
	}

	sub, err := s.CreateSubmission(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != "pending" || sub.EntryID != custom.ID || sub.UserID != userID {
		t.Fatalf("submission wrong: %+v", sub)
	}

	// One pending per entry: the partial unique index answers.
	if _, err := s.CreateSubmission(ctx, userID, custom.ID); !errors.Is(err, store.ErrSubmissionPending) {
		t.Fatalf("double submit = %v, want ErrSubmissionPending", err)
	}

	// Cancel is a status flip; the row persists and a fresh
	// submission is allowed again.
	if err := s.CancelSubmission(ctx, userID, custom.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelSubmission(ctx, userID, custom.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second cancel = %v, want ErrNotFound", err)
	}
	latest, err := s.LatestSubmissionForEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Status != "cancelled" {
		t.Fatalf("latest after cancel = %q", latest.Status)
	}
	sub2, err := s.CreateSubmission(ctx, userID, custom.ID)
	if err != nil {
		t.Fatalf("resubmit after cancel: %v", err)
	}

	// The caps' counting queries: one pending, two created in-window
	// (cancelled rows count - cancel/recreate must not reset the
	// window).
	if n, err := s.CountPendingSubmissions(ctx, userID); err != nil || n != 1 {
		t.Fatalf("pending count = %d (%v), want 1", n, err)
	}
	if n, err := s.CountSubmissionsSince(ctx, userID, time.Now().UTC().Add(-time.Hour)); err != nil || n != 2 {
		t.Fatalf("window count = %d (%v), want 2 incl. cancelled", n, err)
	}
	if n, err := s.CountSubmissionsSince(ctx, userID, time.Now().UTC().Add(time.Hour)); err != nil || n != 0 {
		t.Fatalf("future window = %d (%v), want 0", n, err)
	}

	// The admin queue joins the LIVE entry (edits flow through).
	rows, total, err := s.ListPendingSubmissions(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("queue = %d/%d, want 1/1", len(rows), total)
	}
	row := rows[0]
	if row.ID != sub2.ID || row.DisplayName != custom.DisplayName || row.ItemType != custom.ItemType || row.Region != custom.Region {
		t.Fatalf("proposal join wrong: %+v", row)
	}

	// Reject resolves the row; a second verdict finds it resolved.
	rej, err := s.RejectSubmission(ctx, sub2.ID, "not a shared item")
	if err != nil {
		t.Fatal(err)
	}
	if rej.Status != "rejected" || rej.RejectReason == nil || *rej.RejectReason != "not a shared item" || rej.ReviewedAt == nil {
		t.Fatalf("reject wrong: %+v", rej)
	}
	if _, err := s.RejectSubmission(ctx, sub2.ID, "again"); !errors.Is(err, store.ErrSubmissionResolved) {
		t.Fatalf("re-reject = %v, want ErrSubmissionResolved", err)
	}

	// Approve adopts: the entry flips product-backed and the
	// submission resolves, atomically.
	sub3, err := s.CreateSubmission(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	productID := uuid.New()
	platName := "SNES"
	rd := time.Date(1995, 10, 9, 0, 0, 0, 0, time.UTC)
	if err := s.RecordSubmissionProduct(ctx, sub3.ID, productID); err != nil {
		t.Fatal(err)
	}
	appr, err := s.ApproveSubmission(ctx, sub3.ID, store.CatalogSnapshot{
		ProductID: productID, ItemType: "game", DisplayName: "Curated Name",
		PlatformName: &platName, FirstReleaseDate: &rd,
	})
	if err != nil {
		t.Fatal(err)
	}
	if appr.Status != "approved" || appr.ProductID == nil || *appr.ProductID != productID {
		t.Fatalf("approve wrong: %+v", appr)
	}
	adopted, err := s.GetEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.ProductID == nil || *adopted.ProductID != productID ||
		adopted.DisplayName != "Curated Name" ||
		adopted.PlatformName == nil || *adopted.PlatformName != "SNES" ||
		adopted.FirstReleaseDate == nil || !adopted.FirstReleaseDate.Equal(rd) {
		t.Fatalf("adoption snapshot wrong: %+v", adopted)
	}
	if _, err := s.ApproveSubmission(ctx, sub3.ID, store.CatalogSnapshot{ProductID: productID}); !errors.Is(err, store.ErrSubmissionResolved) {
		t.Fatalf("re-approve = %v, want ErrSubmissionResolved", err)
	}

	// Entry deletion cascades the history away.
	if err := s.DeleteEntry(ctx, userID, custom.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LatestSubmissionForEntry(ctx, userID, custom.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after entry delete = %v, want ErrNotFound", err)
	}
}

// TestCountAllPendingSubmissions pins the review-queue gauge query:
// pending rows count across users, resolved rows do not.
func TestCountAllPendingSubmissions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userA, userB := uuid.New(), uuid.New()

	if n, err := s.CountAllPendingSubmissions(ctx); err != nil || n != 0 {
		t.Fatalf("empty = %d (%v), want 0", n, err)
	}
	entryA := mustCreate(t, s, customEntry(userA), nil)
	entryB := mustCreate(t, s, customEntry(userB), nil)
	if _, err := s.CreateSubmission(ctx, userA, entryA.ID); err != nil {
		t.Fatal(err)
	}
	subB, err := s.CreateSubmission(ctx, userB, entryB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountAllPendingSubmissions(ctx); err != nil || n != 2 {
		t.Fatalf("two users pending = %d (%v), want 2", n, err)
	}
	if _, err := s.RejectSubmission(ctx, subB.ID, "not a shared item"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.CountAllPendingSubmissions(ctx); err != nil || n != 1 {
		t.Fatalf("after reject = %d (%v), want 1", n, err)
	}
}

// TestApproveSubmission_PreservesUserOwnedFields guards ApproveSubmission's
// documented contract - "the entry keeps every user-owned field
// (acquisition, tags, rank, pricing)" - against a regression that widens
// the adoption UPDATE onto a column it must leave alone. The fixture sets
// one value per category (acquisition, condition, pricing, rank) BEFORE
// approval; every one of them must read back unchanged afterward, even
// though the catalog fields did change.
func TestApproveSubmission_PreservesUserOwnedFields(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()

	e := customEntry(userID)
	// Acquisition.
	e.PricePaidCents = new(int64(3599))
	e.Currency = "GBP"
	e.PurchasedAt = new(time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC))
	e.PurchasedFrom = new("Local Game Shop")
	// Condition.
	e.Packaging = "sealed"
	e.HasBox = true
	e.HasManual = true
	e.BoxCondition = new("good")
	e.ManualCondition = new("very_good")
	e.ItemCondition = new("acceptable")
	// Pricing.
	e.PricingMode = "custom"
	e.CustomValueCents = new(int64(4200))
	e.CustomValueEnteredCents = new(int64(5000))
	e.CustomValueEnteredCurrency = new("EUR")
	created := mustCreate(t, s, e, nil)
	origRank := rankOf(t, created) // Rank.

	sub, err := s.CreateSubmission(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	productID := uuid.New()
	platName := "SNES"
	rd := time.Date(1995, 10, 9, 0, 0, 0, 0, time.UTC)
	appr, err := s.ApproveSubmission(ctx, sub.ID, store.CatalogSnapshot{
		ProductID: productID, ItemType: "game", DisplayName: "Curated Name",
		PlatformName: &platName, FirstReleaseDate: &rd,
		LocalizedName: new("聖剣伝説3"), LocalizedNameTranslit: new("Seiken Densetsu 3"),
		LocalizedCoverURL: new("https://images.igdb.example/jp.jpg"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if appr.Status != "approved" {
		t.Fatalf("approve status = %q", appr.Status)
	}

	adopted, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The catalog side actually changed - the preservation check below
	// is meaningless if adoption silently no-oped.
	if adopted.ProductID == nil || *adopted.ProductID != productID ||
		adopted.DisplayName != "Curated Name" ||
		adopted.PlatformName == nil || *adopted.PlatformName != "SNES" ||
		adopted.FirstReleaseDate == nil || !adopted.FirstReleaseDate.Equal(rd) ||
		adopted.LocalizedName == nil || *adopted.LocalizedName != "聖剣伝説3" ||
		adopted.LocalizedNameTranslit == nil || *adopted.LocalizedNameTranslit != "Seiken Densetsu 3" ||
		adopted.LocalizedCoverURL == nil || *adopted.LocalizedCoverURL != "https://images.igdb.example/jp.jpg" {
		t.Fatalf("adoption must write the catalog snapshot: %+v", adopted)
	}

	// Acquisition.
	if adopted.PricePaidCents == nil || *adopted.PricePaidCents != 3599 {
		t.Fatalf("price_paid_cents must survive, got %v", adopted.PricePaidCents)
	}
	if adopted.Currency != "GBP" {
		t.Fatalf("currency must survive, got %q", adopted.Currency)
	}
	if adopted.PurchasedAt == nil || !adopted.PurchasedAt.Equal(*e.PurchasedAt) {
		t.Fatalf("purchased_at must survive, got %v", adopted.PurchasedAt)
	}
	if adopted.PurchasedFrom == nil || *adopted.PurchasedFrom != "Local Game Shop" {
		t.Fatalf("purchased_from must survive, got %v", adopted.PurchasedFrom)
	}
	// Condition.
	if adopted.Packaging != "sealed" {
		t.Fatalf("packaging must survive, got %q", adopted.Packaging)
	}
	if !adopted.HasBox || !adopted.HasManual {
		t.Fatalf("has_box/has_manual must survive, got %v/%v", adopted.HasBox, adopted.HasManual)
	}
	if adopted.BoxCondition == nil || *adopted.BoxCondition != "good" {
		t.Fatalf("box_condition must survive, got %v", adopted.BoxCondition)
	}
	if adopted.ManualCondition == nil || *adopted.ManualCondition != "very_good" {
		t.Fatalf("manual_condition must survive, got %v", adopted.ManualCondition)
	}
	if adopted.ItemCondition == nil || *adopted.ItemCondition != "acceptable" {
		t.Fatalf("item_condition must survive, got %v", adopted.ItemCondition)
	}
	// Pricing.
	if adopted.PricingMode != "custom" {
		t.Fatalf("pricing_mode must survive, got %q", adopted.PricingMode)
	}
	if adopted.CustomValueCents == nil || *adopted.CustomValueCents != 4200 {
		t.Fatalf("custom_value_cents must survive, got %v", adopted.CustomValueCents)
	}
	if adopted.CustomValueEnteredCents == nil || *adopted.CustomValueEnteredCents != 5000 ||
		adopted.CustomValueEnteredCurrency == nil || *adopted.CustomValueEnteredCurrency != "EUR" {
		t.Fatalf("entered custom pair must survive, got %v %v",
			adopted.CustomValueEnteredCents, adopted.CustomValueEnteredCurrency)
	}
	// Rank.
	if rankOf(t, adopted) != origRank {
		t.Fatalf("backlog_rank must survive, got %q want %q", rankOf(t, adopted), origRank)
	}
}

// TestGetSubmission is GetSubmission's direct exercise: a hit returns
// the row keyed on id alone (no user scoping - the admin queue reads
// across users), a miss answers the documented sentinel. GetSubmission
// returns a plain Submission, not a SubmissionProposal, so there are no
// proposal fields to assert here; the live-join proposal fields are
// TestListPendingSubmissions_ReflectsLiveEntryEdits's job below.
func TestGetSubmission(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	entry := mustCreate(t, s, customEntry(userID), nil)

	created, err := s.CreateSubmission(ctx, userID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}

	got, err := s.GetSubmission(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID || got.EntryID != entry.ID || got.UserID != userID || got.Status != "pending" {
		t.Fatalf("get submission = %+v, want id=%s entry=%s user=%s status=pending",
			got, created.ID, entry.ID, userID)
	}

	if _, err := s.GetSubmission(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown id = %v, want ErrNotFound", err)
	}
}

// TestRecordSubmissionProduct_ResolvedGuard: recording on a resolved
// (here, rejected) row must not silently succeed or move product_id -
// the approve_new retry path depends on this guard to detect that a
// concurrent verdict already resolved the row.
func TestRecordSubmissionProduct_ResolvedGuard(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	entry := mustCreate(t, s, customEntry(userID), nil)

	sub, err := s.CreateSubmission(ctx, userID, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RejectSubmission(ctx, sub.ID, "not a shared item"); err != nil {
		t.Fatal(err)
	}

	if err := s.RecordSubmissionProduct(ctx, sub.ID, uuid.New()); !errors.Is(err, store.ErrSubmissionResolved) {
		t.Fatalf("record on a resolved row = %v, want ErrSubmissionResolved", err)
	}

	got, err := s.GetSubmission(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProductID != nil {
		t.Fatalf("product_id must stay nil after a guarded record, got %v", *got.ProductID)
	}
}

// TestListPendingSubmissions_ReflectsLiveEntryEdits proves the queue
// joins the entry LIVE: every mutable proposal column, edited AFTER the
// submission was filed, must show the edit - not a submit-time
// snapshot. item_type is excluded: it is immutable at the store layer
// (UpdateEntry's SET list never touches it), so it cannot be exercised
// this way.
func TestListPendingSubmissions_ReflectsLiveEntryEdits(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	entry := mustCreate(t, s, customEntry(userID), nil)

	if _, err := s.CreateSubmission(ctx, userID, entry.ID); err != nil {
		t.Fatal(err)
	}

	edited := entry
	edited.DisplayName = "Retitled Cart"
	edited.PlatformName = new("SNES (PAL)")
	edited.Region = "pal"
	edited.Edition = new("Player's Choice")
	rd := time.Date(1996, 3, 1, 0, 0, 0, 0, time.UTC)
	edited.FirstReleaseDate = &rd
	if _, err := s.UpdateEntry(ctx, edited, nil); err != nil {
		t.Fatal(err)
	}

	rows, total, err := s.ListPendingSubmissions(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("queue = %d/%d, want 1/1", len(rows), total)
	}
	row := rows[0]
	if row.DisplayName != "Retitled Cart" ||
		row.PlatformName == nil || *row.PlatformName != "SNES (PAL)" ||
		row.Region != "pal" ||
		row.Edition == nil || *row.Edition != "Player's Choice" ||
		row.FirstReleaseDate == nil || !row.FirstReleaseDate.Equal(rd) {
		t.Fatalf("queue must reflect the LIVE entry, got %+v", row)
	}
}

func TestSubmissionAck_StampOnceIdempotentAndApprovedOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	custom, err := s.CreateEntry(ctx, customEntry(userID), nil)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.CreateSubmission(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}

	// No approved submission yet: the approved-only read is ErrNotFound.
	if _, err := s.LatestApprovedSubmissionForEntry(ctx, userID, custom.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("pending-only = %v, want ErrNotFound", err)
	}

	// Approve it (adoption path), then the approved read finds it unstamped.
	if err := s.RecordSubmissionProduct(ctx, sub.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApproveSubmission(ctx, sub.ID, store.CatalogSnapshot{ProductID: uuid.New(), ItemType: "game", DisplayName: "X"}); err != nil {
		t.Fatal(err)
	}
	appr, err := s.LatestApprovedSubmissionForEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if appr.ResolutionAckAt != nil {
		t.Fatalf("fresh approved submission must be unacked: %+v", appr.ResolutionAckAt)
	}

	// Stamp once, then re-stamp is a no-op that does not move the time.
	if err := s.AckSubmissionResolution(ctx, appr.ID); err != nil {
		t.Fatal(err)
	}
	stamped, err := s.LatestApprovedSubmissionForEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stamped.ResolutionAckAt == nil {
		t.Fatal("ack did not stamp")
	}
	first := *stamped.ResolutionAckAt
	if err := s.AckSubmissionResolution(ctx, appr.ID); err != nil {
		t.Fatalf("repeat ack must be a no-op, got %v", err)
	}
	again, err := s.LatestApprovedSubmissionForEntry(ctx, userID, custom.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.ResolutionAckAt == nil || !again.ResolutionAckAt.Equal(first) {
		t.Fatalf("repeat ack moved the stamp: %v -> %v", first, again.ResolutionAckAt)
	}
}

func TestUpdateEntry_RewritesCoverAndPlatformIdForCustom(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	e := customEntry(userID)
	created, err := s.CreateEntry(ctx, e, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Full-replacement update sets both previously write-once fields.
	upd := created
	cover := "https://img.example/custom.jpg"
	pid := int64(19)
	upd.CoverURL = &cover
	upd.PlatformIGDBID = &pid
	name := "SNES"
	upd.PlatformName = &name
	if _, err := s.UpdateEntry(ctx, upd, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CoverURL == nil || *got.CoverURL != cover {
		t.Fatalf("cover not rewritten: %+v", got.CoverURL)
	}
	if got.PlatformIGDBID == nil || *got.PlatformIGDBID != 19 {
		t.Fatalf("platform id not rewritten: %+v", got.PlatformIGDBID)
	}

	// Clearing them (absent in a full replacement) writes NULL.
	upd2 := got
	upd2.CoverURL = nil
	upd2.PlatformIGDBID = nil
	if _, err := s.UpdateEntry(ctx, upd2, nil); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleared.CoverURL != nil || cleared.PlatformIGDBID != nil {
		t.Fatalf("fields not cleared: cover=%v pid=%v", cleared.CoverURL, cleared.PlatformIGDBID)
	}
}

func TestListPendingSubmissions_JoinsCover(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	e := customEntry(userID)
	cover := "https://img.example/prop.jpg"
	e.CoverURL = &cover
	created, err := s.CreateEntry(ctx, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateSubmission(ctx, userID, created.ID); err != nil {
		t.Fatal(err)
	}
	rows, _, err := s.ListPendingSubmissions(ctx, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CoverURL == nil || *rows[0].CoverURL != cover {
		t.Fatalf("proposal cover not joined: %+v", rows)
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
