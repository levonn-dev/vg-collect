// Tests for recommendation scoring.

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/levonn-dev/vgkeep/libs/go/contract/common"
	"github.com/levonn-dev/vgkeep/libs/go/reqtest"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/gen/api"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

// Recommendations

func TestRecommendations_EndToEndOverFixtures(t *testing.T) {
	s := newStack(t)
	// A Zelda/Souls library: candidates come from similar_games edges,
	// exclude owned ids, and carry display metadata fetched into igdb_raw.
	body := map[string]any{"library": []map[string]any{
		{"igdb_game_id": 1001, "rating": 10},        // OoT, loved
		{"igdb_game_id": 1042},                      // Dark Souls
		{"igdb_game_id": 1012, "status": "dropped"}, // Chrono Cross, dropped
	}}
	resp := s.do(http.MethodPost, "/recommendations:score", s.userToken(), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("score: %d", resp.StatusCode)
	}
	out := decodeBody[api.ScoreResponse](t, resp)
	if out.Degraded {
		t.Fatal("stub providers must not degrade")
	}
	if len(out.Recommendations) == 0 {
		t.Fatal("no recommendations")
	}
	seen := map[int64]float64{}
	var linkToPast *common.Recommendation
	for i, rec := range out.Recommendations {
		if rec.IgdbGameId == 1001 || rec.IgdbGameId == 1042 || rec.IgdbGameId == 1012 {
			t.Fatalf("owned id recommended: %d", rec.IgdbGameId)
		}
		if rec.Name == "" || len(rec.Genres) == 0 {
			t.Fatalf("display metadata missing: %+v", rec)
		}
		if i > 0 && out.Recommendations[i-1].Score < rec.Score {
			t.Fatal("not sorted by score desc")
		}
		seen[rec.IgdbGameId] = rec.Score
		if rec.IgdbGameId == 1002 {
			linkToPast = &rec
		}
	}
	// OoT (weight 2.0) links 1002/1003/1004/1035/1037: at least one edge
	// must outrank anything reachable only through dropped Chrono Cross (weight 0.5).
	if _, ok := seen[1002]; !ok {
		t.Fatalf("expected a strong Zelda edge in %v", seen)
	}
	wantDate := time.Date(1991, time.November, 21, 0, 0, 0, 0, time.UTC)
	if linkToPast == nil || linkToPast.FirstReleaseDate == nil || !linkToPast.FirstReleaseDate.Equal(wantDate) {
		t.Fatalf("recommendation first_release_date: %+v", linkToPast)
	}
	// Candidate metadata was populated backwards into igdb_raw.
	raws, err := s.store.RawByIDs(context.Background(), []int64{1002})
	if err != nil || len(raws) != 1 {
		t.Fatalf("igdb_raw candidate population: %d, %v", len(raws), err)
	}
}

func TestRecommendations_EmptyLibrary(t *testing.T) {
	s := newStack(t)
	resp := s.do(http.MethodPost, "/recommendations:score", s.userToken(), map[string]any{"library": []any{}})
	out := decodeBody[api.ScoreResponse](t, resp)
	if out.Degraded || len(out.Recommendations) != 0 {
		t.Fatalf("empty library: %+v", out)
	}
}

func TestRecommendations_SparseLibraryUsesGenreFallback(t *testing.T) {
	s := newStack(t)
	// Pokemon Emerald's only edge is FireRed: the pool is far below the
	// limit, so the genre profile (RPG) must top it up with unowned RPG fixtures.
	body := map[string]any{"library": []map[string]any{{"igdb_game_id": 1020, "rating": 9}}}
	resp := s.do(http.MethodPost, "/recommendations:score", s.userToken(), body)
	out := decodeBody[api.ScoreResponse](t, resp)
	if len(out.Recommendations) < 5 {
		t.Fatalf("fallback did not top up: %d", len(out.Recommendations))
	}
	for _, rec := range out.Recommendations {
		if rec.IgdbGameId == 1020 {
			t.Fatal("owned id recommended by fallback")
		}
	}
}

// Pins the contract's maxItems bound: one entry past the 2500-item
// cap answers 400 before any store/provider call (zero-field stubs
// would panic if reached); specval enforces the cap, not the handler.
func TestUnitRecommendations_LibraryTooLargeRejected(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	h := newUnitHandlers(&stubStore{}, &stubGames{}, nil, newStubCache())
	library := make([]map[string]any, 2501)
	for i := range library {
		library[i] = map[string]any{"igdb_game_id": i + 1}
	}
	rec := serveUnit(t, h, env, http.MethodPost, "/recommendations:score", tok,
		map[string]any{"library": library})
	p := reqtest.AssertProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
	if !strings.Contains(p.Detail, "library") {
		t.Fatalf("want the invalid_body problem to name library, got %q", p.Detail)
	}
}

// Covers the owned-game metadata fetch failing outright: igdb_raw has
// nothing for the owned id, and GamesByIDs is down too, so the first
// ensureRaw call degrades before any candidate/genre logic runs.
func TestUnitRecommendations_DegradedOnMetadataFetchFailure(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) { return nil, nil },
	}
	games := &stubGames{
		gamesByIDs: func(context.Context, []int64) ([]igdb.Game, error) { return nil, errors.New("igdb down") },
	}
	h := newUnitHandlers(st, games, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/recommendations:score", tok,
		map[string]any{"library": []map[string]any{{"igdb_game_id": 1001}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("degraded scoring must still answer: %d %s", rec.Code, rec.Body.String())
	}
	var out api.ScoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Degraded || len(out.Recommendations) != 0 {
		t.Fatalf("want degraded empty answer: %+v", out)
	}
}

// Covers the other degraded trigger: the owned game has a genre but
// no similar_games edges, so the candidate pool stays empty and the
// genre-profile fallback runs, where PopularGames then fails.
func TestUnitRecommendations_DegradedOnGenreFallbackFailure(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	st := &stubStore{
		rawByIDs: func(context.Context, []int64) ([]store.RawGame, error) {
			// Owned, genre but empty similar_games: CandidateIDs stays
			// empty, forcing the genre-profile fallback below.
			return []store.RawGame{{
				GameID: 1001,
				Game:   igdb.Game{ID: 1001, Name: "Owned", Genres: []igdb.Named{{ID: 12, Name: "Role-playing (RPG)"}}},
			}}, nil
		},
	}
	games := &stubGames{
		popularGames: func(context.Context, []int64, []int64, int) ([]igdb.Game, error) {
			return nil, errors.New("igdb down")
		},
	}
	h := newUnitHandlers(st, games, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/recommendations:score", tok,
		map[string]any{"library": []map[string]any{{"igdb_game_id": 1001}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("degraded scoring must still answer: %d %s", rec.Code, rec.Body.String())
	}
	var out api.ScoreResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Degraded || len(out.Recommendations) != 0 {
		t.Fatalf("want a degraded answer from a failed genre fallback: %+v", out)
	}
}

// Pins the contract's bound on limit (1-50): specval rejects an
// out-of-range limit before the handler computes an effective one.
func TestUnitRecommendations_LimitOverMaxRejected(t *testing.T) {
	env := newAuthEnv(t)
	tok := env.token(t, "u1", []string{"user"})
	h := newUnitHandlers(&stubStore{}, &stubGames{}, nil, newStubCache())
	rec := serveUnit(t, h, env, http.MethodPost, "/recommendations:score", tok,
		map[string]any{"library": []map[string]any{{"igdb_game_id": 1}}, "limit": 999})
	reqtest.AssertProblemRec(t, rec, http.StatusBadRequest, "invalid_body")
}
