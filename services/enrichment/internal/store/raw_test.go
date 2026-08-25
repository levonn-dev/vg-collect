package store_test

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

func TestRaw_UpsertReplaceAndMissingAbsent(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC)

	g1 := igdb.Game{ID: 1011, Name: "Chrono Trigger", SimilarGames: []int64{1012}}
	g2 := igdb.Game{ID: 1012, Name: "Chrono Cross"}
	if err := s.UpsertRaw(ctx, []igdb.Game{g1, g2}, at); err != nil {
		t.Fatal(err)
	}

	got, err := s.RawByIDs(ctx, []int64{1011, 1012, 999999})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 raw payloads, got %d", len(got))
	}
	if got[0].Game.Name == "" || !got[0].FetchedAt.Equal(at) {
		t.Fatalf("payload round-trip broken: %+v", got[0])
	}

	// Refetch replaces in place.
	g1.Name = "Chrono Trigger (refetched)"
	later := at.Add(time.Hour)
	if err := s.UpsertRaw(ctx, []igdb.Game{g1}, later); err != nil {
		t.Fatal(err)
	}
	got, _ = s.RawByIDs(ctx, []int64{1011})
	if len(got) != 1 || got[0].Game.Name != "Chrono Trigger (refetched)" || !got[0].FetchedAt.Equal(later) {
		t.Fatalf("replace broken: %+v", got)
	}

	// Empty inputs are clean no-ops.
	if err := s.UpsertRaw(ctx, nil, at); err != nil {
		t.Fatal(err)
	}
	if out, err := s.RawByIDs(ctx, nil); err != nil || out != nil {
		t.Fatalf("empty query should be a no-op: %v, %v", out, err)
	}
}

// The release_dates sentinel is load-bearing: bson-absent reads as
// pre-feature "never fetched", so a nil table normalizes to empty before persisting.
func TestRaw_UpsertNormalizesNilReleaseDatesToEmpty(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC)

	g := igdb.Game{ID: 2001, Name: "Chrono Cross", ReleaseDates: nil}
	if err := s.UpsertRaw(ctx, []igdb.Game{g}, at); err != nil {
		t.Fatal(err)
	}

	got, err := s.RawByIDs(ctx, []int64{2001})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 raw payload, got %d", len(got))
	}
	if got[0].Game.ReleaseDates == nil || len(got[0].Game.ReleaseDates) != 0 {
		t.Fatalf("want a non-nil empty release_dates list, got %+v", got[0].Game.ReleaseDates)
	}
}

func TestRaw_UpsertReplaceDropsFieldsAbsentFromReplacement(t *testing.T) {
	s, mdb := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC)

	// Seed a field RawGame doesn't define (stand-in for a dropped
	// schema field); ReplaceOneModel replaces the whole doc, not $set-merges, so it must vanish.
	if _, err := mdb.Collection("igdb_raw").InsertOne(ctx, bson.D{
		{Key: "_id", Value: int64(1011)},
		{Key: "game", Value: bson.D{{Key: "id", Value: int64(1011)}, {Key: "name", Value: "Chrono Trigger"}}},
		{Key: "fetched_at", Value: at},
		{Key: "legacy_unused_field", Value: "must not survive a replace"},
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertRaw(ctx, []igdb.Game{{ID: 1011, Name: "Chrono Trigger"}}, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var raw bson.M
	if err := mdb.Collection("igdb_raw").FindOne(ctx, bson.D{{Key: "_id", Value: int64(1011)}}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, present := raw["legacy_unused_field"]; present {
		t.Fatalf("ReplaceOneModel must drop fields absent from the replacement document: %+v", raw)
	}
}

// FieldsVersion lets reprojection tell a raw doc fetched under the
// current gameFields generation from one that predates it (refetch, not reproject).
func TestRaw_UpsertStampsFieldsVersion(t *testing.T) {
	s, mdb := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC)

	g := igdb.Game{ID: 3001, Name: "Trials of Mana"}
	if err := s.UpsertRaw(ctx, []igdb.Game{g}, at); err != nil {
		t.Fatal(err)
	}
	got, err := s.RawByIDs(ctx, []int64{3001})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FieldsVersion != store.RawFieldsVersion {
		t.Fatalf("want fields_version %d, got %+v", store.RawFieldsVersion, got)
	}

	// A doc predating this field (no fields_version key) reads back
	// zero: the sentinel the refetch sweep keys on.
	if _, err := mdb.Collection("igdb_raw").InsertOne(ctx, bson.D{
		{Key: "_id", Value: int64(3002)},
		{Key: "game", Value: bson.D{{Key: "id", Value: int64(3002)}, {Key: "name", Value: "Legacy Game"}}},
		{Key: "fetched_at", Value: at},
	}); err != nil {
		t.Fatal(err)
	}
	got, err = s.RawByIDs(ctx, []int64{3002})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].FieldsVersion != 0 {
		t.Fatalf("pre-feature doc must read back fields_version 0, got %+v", got)
	}
}

func TestPlatforms_UpsertListFetchedAt(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	zero, err := s.PlatformsFetchedAt(ctx)
	if err != nil || !zero.IsZero() {
		t.Fatalf("never-fetched must be zero time: %v, %v", zero, err)
	}

	at := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC)
	ps := []igdb.Platform{
		{ID: 19, Name: "Super Nintendo Entertainment System", Abbreviation: "SNES", Generation: 4},
		{ID: 4, Name: "Nintendo 64", Abbreviation: "N64", Generation: 5, PlatformLogo: &igdb.Cover{ImageID: "pl78"}},
	}
	if err := s.UpsertPlatforms(ctx, ps, at); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListPlatforms(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("list: %d, %v", len(got), err)
	}
	if got[0].ID != 4 || got[1].Abbreviation != "SNES" {
		t.Fatalf("unexpected platforms: %+v", got)
	}
	// The precomputed logo_url round-trips; a logo-less platform reads
	// back empty.
	if got[0].LogoURL != "https://images.igdb.com/igdb/image/upload/t_logo_med/pl78.jpg" || got[1].LogoURL != "" {
		t.Fatalf("logo_url round-trip: %+v", got)
	}
	when, err := s.PlatformsFetchedAt(ctx)
	if err != nil || !when.Equal(at) {
		t.Fatalf("fetched_at: %v, %v", when, err)
	}
}
