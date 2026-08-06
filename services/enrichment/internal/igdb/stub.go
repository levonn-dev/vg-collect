package igdb

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed fixtures.json
var fixturesJSON []byte

// Stub serves the embedded fixture catalog: ~50 well-known games with
// interlinked similar_games edges plus the platform table. It keeps
// `task run` and e2e fully credential-less.
type Stub struct {
	games     []Game
	byID      map[int64]Game
	platforms []Platform
}

// NewStub parses the embedded fixtures.
func NewStub() (*Stub, error) {
	var f struct {
		Platforms []Platform `json:"platforms"`
		Games     []Game     `json:"games"`
	}
	if err := json.Unmarshal(fixturesJSON, &f); err != nil {
		return nil, fmt.Errorf("igdb: fixtures: %w", err)
	}
	s := &Stub{games: f.Games, byID: make(map[int64]Game, len(f.Games)), platforms: f.Platforms}
	for _, g := range f.Games {
		s.byID[g.ID] = g
	}
	return s, nil
}

// SearchGames is a case-insensitive substring match over fixture
// names, in fixture order.
func (s *Stub) SearchGames(_ context.Context, q string, limit int) ([]Game, error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	var out []Game
	for _, g := range s.games {
		if strings.Contains(strings.ToLower(g.Name), needle) {
			out = append(out, g)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// SearchLocalizations is a case-insensitive substring match over each
// fixture game's game_localizations[].name, in fixture order (mirrors
// the real endpoint: the game_localizations rows themselves, never the
// merged/translit-mined bundle).
func (s *Stub) SearchLocalizations(_ context.Context, q string, limit int) ([]int64, error) {
	needle := strings.ToLower(strings.TrimSpace(q))
	var out []int64
	for _, g := range s.games {
		for _, loc := range g.GameLocalizations {
			if loc.Name != "" && strings.Contains(strings.ToLower(loc.Name), needle) {
				out = append(out, g.ID)
				break
			}
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// GamesByIDs returns the fixtures it knows; unknown ids are silently
// absent (mirrors the real endpoint).
func (s *Stub) GamesByIDs(_ context.Context, ids []int64) ([]Game, error) {
	out := make([]Game, 0, len(ids))
	for _, id := range ids {
		if g, ok := s.byID[id]; ok {
			out = append(out, g)
		}
	}
	return out, nil
}

// PopularGames returns fixture games in any of the genres, excluding
// the given ids, by total_rating descending (id ascending on ties).
func (s *Stub) PopularGames(_ context.Context, genreIDs []int64, excludeIDs []int64, limit int) ([]Game, error) {
	wantGenre := make(map[int64]bool, len(genreIDs))
	for _, id := range genreIDs {
		wantGenre[id] = true
	}
	excluded := make(map[int64]bool, len(excludeIDs))
	for _, id := range excludeIDs {
		excluded[id] = true
	}
	var out []Game
	for _, g := range s.games {
		if excluded[g.ID] {
			continue
		}
		for _, gen := range g.Genres {
			if wantGenre[gen.ID] {
				out = append(out, g)
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalRating != out[j].TotalRating {
			return out[i].TotalRating > out[j].TotalRating
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Platforms returns the fixture platform table.
func (s *Stub) Platforms(_ context.Context) ([]Platform, error) {
	return append([]Platform(nil), s.platforms...), nil
}
