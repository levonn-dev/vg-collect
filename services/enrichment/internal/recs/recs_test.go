package recs

import "testing"

func intp(v int) *int { return &v }

func TestOwnerWeights_ThroughScore(t *testing.T) {
	// Three owners of the same edge with the three weight cases:
	// rated 9 (2.0), plain (1.0), dropped (0.5). No genre data, so the
	// candidate's score is exactly the weight of its owner.
	meta := map[int64]Meta{
		1: {Similar: []int64{101}},
		2: {Similar: []int64{102}},
		3: {Similar: []int64{103}},
		4: {Similar: []int64{104}},
	}
	lib := []LibraryGame{
		{GameID: 1, Rating: intp(9)},
		{GameID: 2},
		{GameID: 3, Status: "dropped"},
		{GameID: 4, Rating: intp(9), Status: "dropped"}, // dropped wins
	}
	got := Score(lib, meta, []int64{101, 102, 103, 104})
	want := map[int64]float64{101: 2.0, 102: 1.0, 103: 0.5, 104: 0.5}
	if len(got) != 4 {
		t.Fatalf("want 4 scored, got %d", len(got))
	}
	for _, s := range got {
		if want[s.GameID] != s.Score {
			t.Errorf("candidate %d: score %.2f, want %.2f", s.GameID, s.Score, want[s.GameID])
		}
	}
	if got[0].GameID != 101 || got[len(got)-1].GameID != 104 {
		t.Fatalf("ordering broken: %+v (ties must break by id: 103 before 104)", got)
	}
}

func TestScore_EdgesAccumulateAndOwnedExcluded(t *testing.T) {
	meta := map[int64]Meta{
		1: {Similar: []int64{100, 2}}, // 2 is owned: never a candidate
		2: {Similar: []int64{100}},
	}
	lib := []LibraryGame{{GameID: 1, Rating: intp(10)}, {GameID: 2}}
	got := Score(lib, meta, []int64{100, 2, 100}) // dupes and owned filtered
	if len(got) != 1 || got[0].GameID != 100 || got[0].Score != 3.0 {
		t.Fatalf("accumulate/exclude: %+v", got)
	}
}

func TestScore_GenreBonusBreaksTies(t *testing.T) {
	// Candidates 200 and 201 share one edge each (weight 1.0); 200
	// shares the library's only genre, 201 does not.
	meta := map[int64]Meta{
		1:   {Similar: []int64{200, 201}, GenreIDs: []int64{12}},
		200: {GenreIDs: []int64{12}},
		201: {GenreIDs: []int64{99}},
	}
	lib := []LibraryGame{{GameID: 1}}
	got := Score(lib, meta, []int64{201, 200})
	if len(got) != 2 || got[0].GameID != 200 {
		t.Fatalf("genre bonus must rank 200 first: %+v", got)
	}
	if got[0].Score != 1.5 || got[1].Score != 1.0 {
		t.Fatalf("bonus math: %+v (want 1.0 edge + 0.5*1.0 bonus)", got)
	}
}

func TestScore_FallbackCandidatesRankOnBonusAlone(t *testing.T) {
	meta := map[int64]Meta{
		1:   {GenreIDs: []int64{12}}, // owned, no edges at all
		300: {GenreIDs: []int64{12}}, // fallback candidate, shared genre
		301: {GenreIDs: []int64{99}}, // fallback candidate, foreign genre
	}
	lib := []LibraryGame{{GameID: 1}}
	got := Score(lib, meta, []int64{300, 301})
	if len(got) != 1 || got[0].GameID != 300 || got[0].Score != 0.5 {
		t.Fatalf("fallback scoring: %+v (foreign-genre zero-score candidates drop)", got)
	}
}

func TestCandidateIDs_WeightOrderedAndCapped(t *testing.T) {
	meta := map[int64]Meta{
		1: {Similar: []int64{100, 101}},
		2: {Similar: []int64{101, 102, 3}}, // 3 is owned
	}
	lib := []LibraryGame{{GameID: 1, Rating: intp(9)}, {GameID: 2}, {GameID: 3}}
	got := CandidateIDs(lib, meta, 10)
	// 101: 2.0+1.0=3.0; 100: 2.0; 102: 1.0. Owned 3 excluded.
	if len(got) != 3 || got[0] != 101 || got[1] != 100 || got[2] != 102 {
		t.Fatalf("ordering: %v", got)
	}
	if capped := CandidateIDs(lib, meta, 2); len(capped) != 2 || capped[0] != 101 {
		t.Fatalf("cap: %v", capped)
	}
}

func TestTopGenres(t *testing.T) {
	meta := map[int64]Meta{
		1: {GenreIDs: []int64{12, 31}},
		2: {GenreIDs: []int64{12}},
		3: {GenreIDs: []int64{31}},
	}
	lib := []LibraryGame{
		{GameID: 1, Rating: intp(9)},   // 12: +2, 31: +2
		{GameID: 2},                    // 12: +1
		{GameID: 3, Status: "dropped"}, // 31: +0.5
	}
	got := TopGenres(lib, meta, 2)
	if len(got) != 2 || got[0] != 12 || got[1] != 31 {
		t.Fatalf("top genres: %v (12=3.0 must beat 31=2.5)", got)
	}
	if one := TopGenres(lib, meta, 1); len(one) != 1 || one[0] != 12 {
		t.Fatalf("n cap: %v", one)
	}
}

func TestEmptyLibraryScoresNothing(t *testing.T) {
	if got := Score(nil, nil, []int64{1, 2}); len(got) != 0 {
		t.Fatalf("empty library: %+v", got)
	}
	if got := CandidateIDs(nil, nil, 5); len(got) != 0 {
		t.Fatalf("empty candidates: %v", got)
	}
}
