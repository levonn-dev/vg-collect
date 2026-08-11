// Tests for dashboard aggregates: counts, spend, pricing rows, and
// library summary.

package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

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
