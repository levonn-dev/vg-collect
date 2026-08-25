// Tests for entry CRUD, listing, filtering, and bulk mutation.

package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

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

	// The user-owned display fields replace on update, and a proxy carries the recommendation identity (igdb_game_id).
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

// TestEntryLocalizedFieldsPersistThroughCreateAndUpdate covers the localized
// trio: round-trips through create/read, an update rewrites it (a region edit
// re-picks, including back to nothing), and no localization stores NULLs.
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

	// A region edit re-picks: a sparse bundle (cover only) overwrites the whole
	// trio rather than leaving the previous region's title behind.
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

	// No localized presentation means NULLs (ntsc_u, region_free, and every hardware entry).
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

// TestUpdateEntry_PersistsProductRepoint guards the narrow re-match: the
// UPDATE must write product_id on both the returned row and a fresh reload.
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

// TestEntryMatchProvenanceRoundTrip pins match_provenance through INSERT and
// GetEntry in both directions (explicit "user", default "auto"). The store
// threads whatever it's given; it never defaults an empty string itself.
func TestEntryMatchProvenanceRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	picked := baseEntry(user)
	picked.MatchProvenance = "user"
	created := mustCreate(t, s, picked, nil)
	got, err := s.GetEntry(ctx, user, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MatchProvenance != "user" {
		t.Fatalf("match_provenance: got %q, want user", got.MatchProvenance)
	}

	auto := mustCreate(t, s, baseEntry(user), nil)
	got2, err := s.GetEntry(ctx, user, auto.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got2.MatchProvenance != "auto" {
		t.Fatalf("match_provenance: got %q, want auto", got2.MatchProvenance)
	}
}

// TestEntryMatchProvenanceColumnDefault pins the migration's DEFAULT 'auto'
// directly: since CreateEntry always lists match_provenance, only a bare SQL
// INSERT omitting the column exercises it. product_id and a non-backlog status
// are included only to clear the table's other CHECK constraints.
func TestEntryMatchProvenanceColumnDefault(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	userID, productID := uuid.New(), uuid.New()

	var id uuid.UUID
	err := pool.QueryRow(ctx, `
		INSERT INTO entries (user_id, item_type, display_name, region, packaging, product_id, status)
		VALUES ($1, 'game', 'Chrono Trigger', 'ntsc_u', 'cib', $2, 'shelved')
		RETURNING id`, userID, productID).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}

	var got string
	if err := pool.QueryRow(ctx, `SELECT match_provenance FROM entries WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "auto" {
		t.Fatalf("match_provenance default: got %q, want auto", got)
	}
}

// TestAckRegionMismatch_Stamps covers the ack's write: it stamps now() and
// reads back; a foreign caller's ack is a no-op ErrNotFound (same as DeleteEntry).
func TestAckRegionMismatch_Stamps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	created := mustCreate(t, s, baseEntry(userID), nil)
	if created.RegionMismatchAckAt != nil {
		t.Fatal("a fresh entry must be unacked")
	}

	if err := s.AckRegionMismatch(ctx, userID, created.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RegionMismatchAckAt == nil {
		t.Fatal("ack did not stamp region_mismatch_ack_at")
	}

	if err := s.AckRegionMismatch(ctx, uuid.New(), created.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a foreign ack must be ErrNotFound, got %v", err)
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

// TestListFilters_CreditOverlap pins the credit dimensions: arrays round-trip
// through create, a filter value matches any entry whose array contains it
// (overlap OR within a dimension, AND across dimensions); uncredited entries never match.
func TestListFilters_CreditOverlap(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	mk := func(name string, devs, pubs []string) store.Entry {
		t.Helper()
		e := baseEntry(user)
		e.DisplayName = name
		e.Developers = devs
		e.Publishers = pubs
		return mustCreate(t, s, e, nil)
	}
	prime := mk("Metroid Prime", []string{"Retro Studios", "Nintendo"}, []string{"Nintendo"})
	mk("Chrono Trigger", []string{"Square"}, []string{"Square"})
	uncredited := mk("Homebrew Cart", nil, nil)

	if len(prime.Developers) != 2 || prime.Developers[0] != "Retro Studios" || prime.Developers[1] != "Nintendo" {
		t.Fatalf("created entry developers = %v, want the two-studio array round-tripped", prime.Developers)
	}
	if uncredited.Developers != nil || uncredited.Publishers != nil {
		t.Fatalf("uncredited entry must round-trip nil arrays, got %v/%v", uncredited.Developers, uncredited.Publishers)
	}

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
	f.Developers = []string{"Nintendo"}
	wantNames(t, list(f), "Metroid Prime")

	f = base
	f.Developers = []string{"Square", "Retro Studios"} // OR within the dimension
	wantNames(t, list(f), "Chrono Trigger", "Metroid Prime")

	f = base
	f.Publishers = []string{"Nintendo"}
	wantNames(t, list(f), "Metroid Prime")

	f = base
	f.Developers, f.Publishers = []string{"Nintendo"}, []string{"Square"} // AND across
	if got := list(f); len(got) != 0 {
		t.Fatalf("expected empty (no entry is developed by Nintendo AND published by Square), got %v", names(got))
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

	// backlog_rank is the one sort WITHOUT the pinned prefix: pure rank order (chrono before terra).
	f := store.Filters{Sort: "backlog_rank", Order: "asc"}
	f.Statuses = []string{"backlog"}
	wantNames(t, list(f), "Chrono Trigger", "Terranigma")

	// The value sort falls back to the stable base order here (the handler re-sorts after price composition).
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

// TestListEntriesPage pins the SQL LIMIT/OFFSET pushdown: a page is the right
// slice of the ordered set, an offset past the end is empty, and tags still ride along.
func TestListEntriesPage(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user, _, _ := seedMatrix(t, s)
	page := func(limit, offset int) []store.Entry {
		t.Helper()
		got, err := s.ListEntriesPage(ctx, user, store.Filters{Sort: "name", Order: "asc"}, limit, offset)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// Full name-asc order: Terranigma (pinned), Alundra, Chrono Trigger, Controller, Super Nintendo.
	wantNames(t, page(2, 1), "Alundra", "Chrono Trigger")
	wantNames(t, page(2, 4), "Super Nintendo")
	if got := page(2, 99); len(got) != 0 {
		t.Fatalf("offset past the end must be empty, got %d", len(got))
	}
	for _, e := range page(5, 0) {
		if e.Tags == nil {
			t.Fatal("tags must never be nil")
		}
	}
}

// TestBulkUpdateEntries_ScalarActionsAndOwnershipFiltering pins the flat
// scalar update (status/storage_location; bulk-update's own clearing rule: an
// empty string clears, absent leaves untouched, opposite of full-replacement)
// plus ownership filtering: foreign/unknown ids are silently excluded.
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

	// Empty string clears; a field absent from THIS call leaves the column untouched (status stays "shelved").
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

// TestBulkUpdateEntries_EnteringBacklogAppendsAndPreservesPosition: entries
// newly entering backlog get a fresh rank appended at the end (oldest-first in
// the batch); an entry already in backlog keeps its existing rank.
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

// TestBulkUpdateEntries_TagAddRemove pins the tag delta: add_tag_ids attaches
// to every owned entry (a foreign tag id matches nothing), remove_tag_ids detaches.
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

// TestBulkUpdateEntries_PerEntryTagCapRollsBackWholeTransaction: one entry
// crossing the 50-tag ceiling rolls back the ENTIRE transaction, including
// changes to an entry that alone would have stayed under the cap.
func TestBulkUpdateEntries_PerEntryTagCapRollsBackWholeTransaction(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := uuid.New()

	// 53 distinct tags: 48 pre-attached at store level (bypasses the handler's
	// 50 max on tag_ids), 5 new ones this call adds.
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
// bulk-update reports the same updated_count even when every action was already true.
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
