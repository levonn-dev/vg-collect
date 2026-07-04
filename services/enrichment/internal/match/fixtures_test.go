package match_test

import (
	"context"
	"testing"

	"github.com/levonn-dev/vg-collect/services/enrichment/internal/igdb"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/match"
	"github.com/levonn-dev/vg-collect/services/enrichment/internal/pricecharting"
)

// TestFixtures_EveryGameResolvesExceptTheUnmatchedOne drives each
// fixture game through the same search+score path the resolve handler
// uses. It pins the fixture datasets to the scorer: renaming a fixture
// on either side, or lowering the fixture count, fails here.
func TestFixtures_EveryGameResolvesExceptTheUnmatchedOne(t *testing.T) {
	ctx := context.Background()
	games, err := igdb.NewStub()
	if err != nil {
		t.Fatal(err)
	}
	prices, err := pricecharting.NewStub()
	if err != nil {
		t.Fatal(err)
	}

	all, err := games.SearchGames(ctx, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 50 {
		t.Fatalf("want the full fixture catalog, got %d", len(all))
	}

	const unmatchedFixture = "Terranigma"
	unmatchedSeen := false
	for _, g := range all {
		hits, err := prices.Search(ctx, g.Name)
		if err != nil {
			t.Fatal(err)
		}
		cands := make([]match.Candidate, 0, len(hits))
		for _, h := range hits {
			cands = append(cands, match.Candidate{PCProductID: h.ID, Name: h.Name, ConsoleName: h.ConsoleName})
		}
		res := match.Best(g.Name, "", g.Platforms[0].Name, cands)
		if g.Name == unmatchedFixture {
			unmatchedSeen = true
			if res.OK {
				t.Fatalf("%s is the designated unmatched fixture but matched %d", g.Name, res.PCProductID)
			}
			continue
		}
		if !res.OK {
			t.Errorf("fixture game %q (platform %q) failed to match: best confidence %.2f over %d candidates",
				g.Name, g.Platforms[0].Name, res.Confidence, len(cands))
			continue
		}
		// Game 1NNN pairs with product 5NNN by construction.
		if res.PCProductID != g.ID+4000 {
			t.Errorf("fixture game %q matched %d, want %d", g.Name, res.PCProductID, g.ID+4000)
		}
	}
	if !unmatchedSeen {
		t.Fatal("the unmatched fixture is missing from the game fixtures")
	}
}
