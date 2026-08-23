package store_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/levonn-dev/vgkeep/libs/go/pgkit"
	"github.com/levonn-dev/vgkeep/libs/go/pgtest"
	"github.com/levonn-dev/vgkeep/services/collection/internal/store"
	"github.com/levonn-dev/vgkeep/services/collection/migrations"
)

// newTestPool: a fresh, fully migrated database and its own pool.
// newTestStore wraps it for the common case; the rare test needing SQL
// the Store's own methods cannot express (a bare INSERT omitting a
// DEFAULTed column) takes the pool directly.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	url := pgtest.URL(t)
	// Reset: drop everything the previous test left (schema_migrations
	// included) and re-run the embedded migrations, so each test opens
	// on a fresh, fully migrated database - migration-seeded rows and
	// all. Two Execs because pgx's extended protocol takes one
	// statement at a time.
	conn, err := pgx.Connect(ctx, url)
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
	if err := pgkit.Migrate(url, migrations.FS, "."); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgkit.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	return store.New(newTestPool(t))
}

// baseEntry is a minimal valid game entry for userID; tests override
// the fields under test.
func baseEntry(userID uuid.UUID) store.Entry {
	return store.Entry{
		UserID:          userID,
		ProductID:       new(uuid.New()),
		ItemType:        "game",
		MediaType:       "physical",
		DisplayName:     "Chrono Trigger",
		Region:          "ntsc_u",
		Packaging:       "cib",
		Currency:        "USD",
		PricingMode:     "auto",
		MatchProvenance: "auto",
		Status:          "backlog",
		Source:          "manual",
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

// TestRegionMismatchAck_ClearsOnChoiceChange exercises the once-per-
// choice reset rule through the real store: a plain edit (notes/
// status only) carries the stamp forward, while a region change, a
// product change riding UpdateEntry (the narrow re-match arm and the
// region-arm repoint both ride this exact statement - the store
// cannot tell them apart, and does not need to), and RepointEntry
// (the entry rematch's own write) each clear it back to nil.
func TestRegionMismatchAck_ClearsOnChoiceChange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	userID := uuid.New()
	created := mustCreate(t, s, baseEntry(userID), nil)

	ack := func(t *testing.T) store.Entry {
		t.Helper()
		if err := s.AckRegionMismatch(ctx, userID, created.ID); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetEntry(ctx, userID, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.RegionMismatchAckAt == nil {
			t.Fatal("ack did not stamp")
		}
		return got
	}

	// Plain edit: the stamp carries forward.
	acked := ack(t)
	acked.Notes = new("great condition")
	acked.Status = "playing"
	plain, err := s.UpdateEntry(ctx, acked, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain.RegionMismatchAckAt == nil || !plain.RegionMismatchAckAt.Equal(*acked.RegionMismatchAckAt) {
		t.Fatalf("a plain edit must carry the stamp forward: got %v, want %v", plain.RegionMismatchAckAt, acked.RegionMismatchAckAt)
	}

	// Region change alone clears it.
	acked = ack(t)
	acked.Region = "pal"
	regionChanged, err := s.UpdateEntry(ctx, acked, nil)
	if err != nil {
		t.Fatal(err)
	}
	if regionChanged.RegionMismatchAckAt != nil {
		t.Fatal("a region change must clear the stamp")
	}

	// Product change via UpdateEntry (the narrow re-match arm and the
	// region-arm repoint both land here) clears it.
	acked = ack(t)
	newProduct := uuid.New()
	acked.ProductID = &newProduct
	productChanged, err := s.UpdateEntry(ctx, acked, nil)
	if err != nil {
		t.Fatal(err)
	}
	if productChanged.RegionMismatchAckAt != nil {
		t.Fatal("a product change via UpdateEntry must clear the stamp")
	}

	// RepointEntry (the entry rematch's own write) clears it too.
	acked = ack(t)
	if err := s.RepointEntry(ctx, created.ID, uuid.New(), nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	repointed, err := s.GetEntry(ctx, userID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if repointed.RegionMismatchAckAt != nil {
		t.Fatal("RepointEntry must clear the stamp")
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

// insertEntryWithRegion creates a minimal product-backed entry with
// region overridden - free text or a known value, per the caller. No
// raw INSERT needed: region has no CHECK restricting it to known
// values (open-world since migration 000013), so CreateEntry accepts
// it directly.
func insertEntryWithRegion(t *testing.T, s *store.Store, region string) store.Entry {
	t.Helper()
	e := baseEntry(uuid.New())
	e.Region = region
	return mustCreate(t, s, e, nil)
}
