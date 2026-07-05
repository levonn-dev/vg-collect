package igdb

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newStub(t *testing.T) *Stub {
	t.Helper()
	s, err := NewStub()
	if err != nil {
		t.Fatalf("NewStub: %v", err)
	}
	return s
}

func TestStub_FixtureShape(t *testing.T) {
	s := newStub(t)
	if len(s.games) != 50 {
		t.Fatalf("want 50 fixture games, got %d", len(s.games))
	}
	if len(s.platforms) != 17 {
		t.Fatalf("want 17 fixture platforms, got %d", len(s.platforms))
	}
	platformIDs := map[int64]bool{}
	for _, p := range s.platforms {
		platformIDs[p.ID] = true
	}
	seen := map[int64]bool{}
	for _, g := range s.games {
		if seen[g.ID] {
			t.Fatalf("duplicate fixture game id %d", g.ID)
		}
		seen[g.ID] = true
		if g.Name == "" || len(g.Genres) == 0 || g.FirstReleaseDate == 0 {
			t.Fatalf("game %d missing core fields", g.ID)
		}
		if g.Cover == nil || !strings.HasPrefix(g.Cover.ImageID, "co_fx") {
			t.Fatalf("game %d missing fixture cover", g.ID)
		}
		if len(g.Platforms) == 0 {
			t.Fatalf("game %d has no platform", g.ID)
		}
		for _, p := range g.Platforms {
			if !platformIDs[p.ID] {
				t.Fatalf("game %d references platform %d not in the fixture platform table", g.ID, p.ID)
			}
		}
		// The similar_games graph must be closed over the fixture set:
		// recommendations candidates always resolve.
		for _, sg := range g.SimilarGames {
			if _, ok := s.byID[sg]; !ok {
				t.Fatalf("game %d similar_games references %d, absent from fixtures", g.ID, sg)
			}
		}
	}
}

func TestStub_SearchGames(t *testing.T) {
	s := newStub(t)
	got, err := s.SearchGames(context.Background(), "zelda", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("want 4 zelda fixtures, got %d", len(got))
	}
	got, err = s.SearchGames(context.Background(), "ZELDA", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("limit not applied: got %d", len(got))
	}
	got, err = s.SearchGames(context.Background(), "no such fixture game", 20)
	if err != nil || len(got) != 0 {
		t.Fatalf("want empty result, got %d (%v)", len(got), err)
	}
}

func TestStub_GamesByIDs_UnknownAbsent(t *testing.T) {
	s := newStub(t)
	got, err := s.GamesByIDs(context.Background(), []int64{1001, 999999, 1044})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != 1001 || got[1].ID != 1044 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestStub_PopularGames_SortedAndFiltered(t *testing.T) {
	s := newStub(t)
	got, err := s.PopularGames(context.Background(), []int64{12}, []int64{1011}, 5)
	if err != nil {
		t.Fatal(err)
	}
	// Independently verified against fixtures.json: every genre-12 game
	// except the excluded 1011, sorted by total_rating desc with id asc
	// as the tiebreak PopularGames itself applies (the fixture data hits
	// a three-way 93.0 tie among 1002/1014/1021, which is why the id
	// tiebreak matters for this exact sequence to be deterministic).
	wantIDs := []int64{1001, 1004, 1002, 1014, 1021}
	if len(got) != len(wantIDs) {
		t.Fatalf("want %d games, got %d: %+v", len(wantIDs), len(got), got)
	}
	for i, g := range got {
		if g.ID != wantIDs[i] {
			t.Fatalf("position %d: want id %d, got id %d", i, wantIDs[i], g.ID)
		}
		if g.ID == 1011 {
			t.Fatal("excluded id returned")
		}
		hasGenre := false
		for _, gen := range g.Genres {
			if gen.ID == 12 {
				hasGenre = true
				break
			}
		}
		if !hasGenre {
			t.Fatalf("game %d missing requested genre 12: %+v", g.ID, g.Genres)
		}
		if i > 0 && got[i-1].TotalRating < g.TotalRating {
			t.Fatal("not sorted by rating desc")
		}
	}
}

func TestStub_Platforms(t *testing.T) {
	s := newStub(t)
	got, err := s.Platforms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 17 {
		t.Fatalf("want 17 fixture platforms, got %d", len(got))
	}

	var found bool
	for _, p := range got {
		if p.ID == 18 {
			found = true
			if p.Name != "Nintendo Entertainment System" {
				t.Fatalf("platform 18: want name %q, got %q", "Nintendo Entertainment System", p.Name)
			}
			break
		}
	}
	if !found {
		t.Fatal("fixture platform 18 not found")
	}

	// Defensive copy: mutating the returned slice must not leak into the
	// stub's backing data or a subsequent call's result.
	original := got[0].Name
	got[0].Name = "mutated"
	again, err := s.Platforms(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again[0].Name != original {
		t.Fatalf("Platforms() is not a defensive copy: want %q, got %q", original, again[0].Name)
	}
}

func TestGame_Helpers(t *testing.T) {
	s := newStub(t)
	g := s.byID[1001]
	if got := g.CoverURL(); got != "https://images.igdb.com/igdb/image/upload/t_cover_big/co_fx1001.jpg" {
		t.Fatalf("CoverURL: %s", got)
	}
	want := time.Date(1998, time.November, 21, 0, 0, 0, 0, time.UTC)
	if got := g.ReleaseDate(); !got.Equal(want) {
		t.Fatalf("ReleaseDate: %v, want %v", got, want)
	}
	if got := (Game{}).CoverURL(); got != "" {
		t.Fatalf("empty cover should be empty URL, got %q", got)
	}
	if got := (Game{}).ReleaseDate(); !got.IsZero() {
		t.Fatalf("unset release date should be zero time, got %v", got)
	}
}

func TestPlatform_LogoURL(t *testing.T) {
	s := newStub(t)
	var nes Platform
	var found bool
	for _, p := range s.platforms {
		if p.ID == 18 {
			nes = p
			found = true
			break
		}
	}
	if !found {
		t.Fatal("fixture platform 18 not found")
	}
	if got := nes.LogoURL(); got != "https://images.igdb.com/igdb/image/upload/t_logo_med/pl_fx18.jpg" {
		t.Fatalf("LogoURL: %s", got)
	}
	if got := (Platform{}).LogoURL(); got != "" {
		t.Fatalf("empty logo should be empty URL, got %q", got)
	}
}
