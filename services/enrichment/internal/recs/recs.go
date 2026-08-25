// Package recs implements the heuristic recommendation scoring: pure
// data-in/data-out, user-agnostic (the library summary is never stored).
package recs

import "sort"

// LibraryGame is one owned game in the caller's summary.
type LibraryGame struct {
	GameID int64
	Rating *int
	Status string
}

// Meta is the per-game metadata scoring needs (from igdb_raw).
type Meta struct {
	Similar  []int64
	GenreIDs []int64
}

// Scored is one ranked candidate.
type Scored struct {
	GameID int64
	Score  float64
}

// ownerWeight: dropped 0.5; rating >= 8 lifts to 2.0; otherwise 1.0.
// Dropped wins over a high rating (abandonment is the stronger signal).
func ownerWeight(g LibraryGame) float64 {
	if g.Status == "dropped" {
		return 0.5
	}
	if g.Rating != nil && *g.Rating >= 8 {
		return 2.0
	}
	return 1.0
}

func ownedSet(lib []LibraryGame) map[int64]bool {
	owned := make(map[int64]bool, len(lib))
	for _, g := range lib {
		owned[g.GameID] = true
	}
	return owned
}

// CandidateIDs gathers similar_games edges from the library, excludes
// owned ids, returns them by weight desc/id asc, capped (bounds fetch budget).
func CandidateIDs(lib []LibraryGame, meta map[int64]Meta, cap int) []int64 {
	owned := ownedSet(lib)
	weight := map[int64]float64{}
	for _, g := range lib {
		m, ok := meta[g.GameID]
		if !ok {
			continue
		}
		w := ownerWeight(g)
		for _, s := range m.Similar {
			if !owned[s] {
				weight[s] += w
			}
		}
	}
	ids := make([]int64, 0, len(weight))
	for id := range weight {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if weight[ids[i]] != weight[ids[j]] {
			return weight[ids[i]] > weight[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > cap {
		ids = ids[:cap]
	}
	return ids
}

// TopGenres returns the library's most frequent genre ids (weighted by
// owner weight, ties by id), up to n: the genre-profile fallback's query input.
func TopGenres(lib []LibraryGame, meta map[int64]Meta, n int) []int64 {
	weight := map[int64]float64{}
	for _, g := range lib {
		m, ok := meta[g.GameID]
		if !ok {
			continue
		}
		w := ownerWeight(g)
		for _, gen := range m.GenreIDs {
			weight[gen] += w
		}
	}
	ids := make([]int64, 0, len(weight))
	for id := range weight {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if weight[ids[i]] != weight[ids[j]] {
			return weight[ids[i]] > weight[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) > n {
		ids = ids[:n]
	}
	return ids
}

// Score ranks candidates. Edge score: sum of owner weights of library
// games whose similar_games include the candidate. Genre bonus: 0.5 x
// the weighted fraction of the library sharing each genre (fallback
// candidates with no edges rank on this alone). Deterministic: score desc, id asc.
func Score(lib []LibraryGame, meta map[int64]Meta, candidates []int64) []Scored {
	owned := ownedSet(lib)

	edge := map[int64]float64{}
	genreWeight := map[int64]float64{}
	var totalWeight float64
	for _, g := range lib {
		m, ok := meta[g.GameID]
		if !ok {
			continue
		}
		w := ownerWeight(g)
		totalWeight += w
		for _, s := range m.Similar {
			edge[s] += w
		}
		for _, gen := range m.GenreIDs {
			genreWeight[gen] += w
		}
	}

	seen := map[int64]bool{}
	out := make([]Scored, 0, len(candidates))
	for _, c := range candidates {
		if owned[c] || seen[c] {
			continue
		}
		seen[c] = true
		score := edge[c]
		if m, ok := meta[c]; ok && totalWeight > 0 {
			var bonus float64
			for _, gen := range m.GenreIDs {
				bonus += genreWeight[gen] / totalWeight
			}
			score += 0.5 * bonus
		}
		if score > 0 {
			out = append(out, Scored{GameID: c, Score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].GameID < out[j].GameID
	})
	return out
}
