package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/levonn-dev/vg-collect/services/enrichment/internal/igdb"
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
