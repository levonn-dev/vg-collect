package pricecharting

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newStubAt(t *testing.T, at time.Time) *Stub {
	t.Helper()
	s, err := NewStub()
	if err != nil {
		t.Fatalf("NewStub: %v", err)
	}
	s.now = func() time.Time { return at }
	return s
}

func TestStub_FixtureShape(t *testing.T) {
	s := newStubAt(t, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	if len(s.products) != 67 {
		t.Fatalf("want 67 fixture products (50 game slots minus the unmatched one, plus 3 alias-corpus fillers, plus 1 variant listing, plus 8 hardware, plus 5 region-alias fillers, plus 1 region hardware), got %d", len(s.products))
	}
	if _, ok := s.byID[5018]; ok {
		t.Fatal("5018 must not exist: Terranigma is the designated unmatched fixture")
	}
	hardware := 0
	for _, p := range s.products {
		if p.Name == "" || p.ConsoleName == "" {
			t.Fatalf("product %d missing core fields", p.ID)
		}
		switch p.Genre {
		case "Systems", "Controllers", "Accessories":
			hardware++
		case "", "RPG", "Adventure":
		default:
			t.Fatalf("product %d has unexpected genre %q", p.ID, p.Genre)
		}
	}
	if hardware != 9 {
		t.Fatalf("want 9 hardware fixtures, got %d", hardware)
	}
}

func TestStub_PriceWalkDeterministic(t *testing.T) {
	day1 := time.Date(2026, 7, 1, 3, 0, 0, 0, time.UTC)
	day1later := time.Date(2026, 7, 1, 23, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 7, 2, 3, 0, 0, 0, time.UTC)

	a, err := newStubAt(t, day1).Product(context.Background(), 5011)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newStubAt(t, day1later).Product(context.Background(), 5011)
	if *a.LoosePriceCents != *b.LoosePriceCents {
		t.Fatal("same UTC day must price identically (restart-stable)")
	}
	if *a.CIBPriceCents != *a.LoosePriceCents*8/5 || *a.NewPriceCents != *a.LoosePriceCents*11/4 {
		t.Fatal("condition multipliers broken")
	}
	if *a.LoosePriceCents < 425 || *a.LoosePriceCents > 17249 {
		t.Fatalf("price outside the walk envelope: %d", *a.LoosePriceCents)
	}

	// Different products price differently (id-seeded).
	c, _ := newStubAt(t, day1).Product(context.Background(), 5013)
	if *c.LoosePriceCents == *a.LoosePriceCents {
		t.Fatal("distinct ids should not share a price curve point")
	}

	// The walk moves across at least one nearby day (period is at
	// least 20 days, so two consecutive days on a sine can't both be stationary).
	d, _ := newStubAt(t, day2).Product(context.Background(), 5011)
	e, _ := newStubAt(t, day2.Add(24*time.Hour)).Product(context.Background(), 5011)
	if *d.LoosePriceCents == *a.LoosePriceCents && *e.LoosePriceCents == *d.LoosePriceCents {
		t.Fatal("walk is flat across three days; snapshots would never move")
	}
}

func TestStub_SearchAndNotFound(t *testing.T) {
	s := newStubAt(t, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	got, err := s.Search(context.Background(), "zelda")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 {
		t.Fatalf("want 6 zelda products, got %d", len(got))
	}
	for _, p := range got {
		if p.LoosePriceCents == nil {
			t.Fatal("search results must carry prices")
		}
	}
	if _, err := s.Product(context.Background(), 5018); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for the unmatched fixture id, got %v", err)
	}
}

// Resolve queries this stub with full IGDB spellings; the fold must
// bridge article/punctuation/subtitle differences in both directions.
func TestStub_SearchFoldsIGDBSpellings(t *testing.T) {
	s := newStubAt(t, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	got, err := s.Search(context.Background(), "The Legend of Zelda: Ocarina of Time")
	if err != nil || len(got) != 1 || got[0].ID != 5001 {
		t.Fatalf("full IGDB name should find 5001: %+v, %v", got, err)
	}
	got, err = s.Search(context.Background(), "Pokemon FireRed Version")
	if err != nil || len(got) != 1 || got[0].ID != 5019 {
		t.Fatalf("longer query than product name should still hit 5019: %+v, %v", got, err)
	}
	got, err = s.Search(context.Background(), "Terranigma")
	if err != nil || len(got) != 0 {
		t.Fatalf("the unmatched fixture must have no products: %+v, %v", got, err)
	}
}
