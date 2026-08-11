// Tests for saved views and the shared-shelf surface.

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
)

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
