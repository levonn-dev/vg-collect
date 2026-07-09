package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/levonn-dev/vg-collect/libs/go/pgkit"
	"github.com/levonn-dev/vg-collect/services/collection/internal/store"
	"github.com/levonn-dev/vg-collect/services/collection/migrations"
)

// newTestStore duplicates the fixture in migrations/migrations_test.go
// (Go test packages can't share helpers across packages).
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	if testing.Short() {
		t.Skip("requires docker")
	}
	ctx := context.Background()
	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("collection"), tcpostgres.WithUsername("c"), tcpostgres.WithPassword("p"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })
	url, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgkit.Connect(ctx, url)
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
		ProductID:   ptr(uuid.New()),
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
	e.PlatformName = ptr("SNES")
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
	created.PlatformName = ptr("Super Famicom")
	created.FirstReleaseDate = &released
	created.PricingMode = "proxy"
	created.PricingProductID = ptr(uuid.New())
	created.IGDBGameID = ptr(int64(1000))
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
	created := mustCreate(t, s, e, nil)
	if created.CoverURL == nil || *created.CoverURL != cover {
		t.Fatalf("create must return the cover snapshot, got %v", created.CoverURL)
	}
	got, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CoverURL == nil || *got.CoverURL != cover {
		t.Fatalf("read back: %v", got.CoverURL)
	}

	// Null stays null (customs and hardware never set one).
	bare := mustCreate(t, s, baseEntry(userID), nil)
	if bare.CoverURL != nil {
		t.Fatalf("no snapshot means null, got %v", *bare.CoverURL)
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
	a.Notes = ptr("still first")
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

func ptr[T any](v T) *T { return &v }

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
//	chrono  game      SNES(6) backlog  cib    ntsc_u  rating 9  paid 5000 USD  tags rpg,fav  year 1995
//	alundra game      PS1(7)  playing  loose  pal     no rating no paid        tags rpg      year 1997
//	snes    console   SNES(6) shelved  cib    ntsc_u  no rating paid 12000 USD no tags       no year
//	terra   game      SNES(6) backlog  sealed ntsc_j  rating 3  no paid        tags fav      year 1996  PINNED
//	pad     accessory (none)  shelved  loose  region_free       paid 2000 EUR  no tags       no year
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
		e.PlatformIGDBID, e.PlatformName = ptr(int64(6)), ptr("SNES")
		e.IGDBGameID = ptr(int64(1000))
		e.FirstReleaseDate = ptr(time.Date(1995, time.March, 11, 0, 0, 0, 0, time.UTC))
		e.Rating, e.PricePaidCents = ptr(9), ptr(int64(5000))
	}, rpg.ID, fav.ID)
	byName["alundra"] = mk("Alundra", func(e *store.Entry) {
		e.PlatformIGDBID, e.PlatformName = ptr(int64(7)), ptr("PS1")
		e.IGDBGameID = ptr(int64(1001))
		e.FirstReleaseDate = ptr(time.Date(1997, time.April, 11, 0, 0, 0, 0, time.UTC))
		e.Status, e.Packaging, e.Region = "playing", "loose", "pal"
	}, rpg.ID)
	byName["snes"] = mk("Super Nintendo", func(e *store.Entry) {
		e.ItemType = "console"
		e.PlatformIGDBID, e.PlatformName = ptr(int64(6)), ptr("SNES")
		e.Status, e.PricePaidCents = "shelved", ptr(int64(12000))
	})
	byName["terra"] = mk("Terranigma", func(e *store.Entry) {
		e.PlatformIGDBID, e.PlatformName = ptr(int64(6)), ptr("SNES")
		e.IGDBGameID = ptr(int64(1002))
		e.FirstReleaseDate = ptr(time.Date(1996, time.October, 19, 0, 0, 0, 0, time.UTC))
		e.Packaging, e.Region = "sealed", "ntsc_j"
		e.Rating, e.Pinned = ptr(3), true
	}, fav.ID)
	byName["pad"] = mk("Controller", func(e *store.Entry) {
		e.ItemType = "accessory"
		e.Status, e.Packaging, e.Region = "shelved", "loose", "region_free"
		e.PricePaidCents, e.Currency = ptr(int64(2000)), "EUR"
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
	c1.IGDBGameID, c1.Status, c1.Rating = ptr(int64(2000)), "dropped", ptr(5)
	mustCreate(t, s, c1, nil)
	c2 := baseEntry(user)
	c2.IGDBGameID, c2.Status, c2.Rating = ptr(int64(2000)), "playing", ptr(8)
	mustCreate(t, s, c2, nil)

	// One fully dropped game, unrated.
	d := baseEntry(user)
	d.IGDBGameID, d.Status = ptr(int64(2001)), "dropped"
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

func TestViewsCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, stranger := uuid.New(), uuid.New()
	params := []byte(`{"filters":{"status":["backlog"]},"sort":"rating","view_mode":"grid"}`)

	v, err := s.CreateView(ctx, user, "Backlog by rating", params)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateView(ctx, user, "backlog BY RATING", params); !errors.Is(err, store.ErrNameTaken) {
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
	updated, err := s.UpdateView(ctx, user, v.ID, "Shelf", newParams)
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
	if _, err := s.UpdateView(ctx, stranger, v.ID, "x", params); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign update must be ErrNotFound, got %v", err)
	}

	if err := s.DeleteView(ctx, user, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteView(ctx, user, v.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("double delete must be ErrNotFound, got %v", err)
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
	strangerEntry.PricePaidCents = ptr(int64(99999))
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
	if _, err := s.CreateView(ctx, a, "Backlog", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}

	bTag, err := s.CreateTag(ctx, b, "rpg")
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, baseEntry(b), []uuid.UUID{bTag.ID})
	if _, err := s.CreateView(ctx, b, "Backlog", []byte(`{}`)); err != nil {
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
