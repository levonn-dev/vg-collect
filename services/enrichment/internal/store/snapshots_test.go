package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

func TestSnapshots_AppendIntoTimeSeries(t *testing.T) {
	s, pool := newTestStore(t)
	ctx := context.Background()

	// price_snapshots FKs to products, so each series needs a real row.
	p1, err := s.CreateProduct(ctx, gameProduct(4200, 19, "Chrono Trigger", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.CreateProduct(ctx, gameProduct(4201, 19, "Chrono Cross", "SNES", ""))
	if err != nil {
		t.Fatal(err)
	}

	loose := int64(4200)
	base := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC)
	for day := range 3 {
		v := loose + int64(day)*10
		snap := store.Snapshot{
			ProductID:  p1.ID,
			CapturedAt: base.AddDate(0, 0, day),
			LooseCents: &v,
		}
		if err := s.AppendSnapshot(ctx, snap); err != nil {
			t.Fatal(err)
		}
	}
	// One point for a different product, to prove the query filters by product_id.
	other := int64(100)
	if err := s.AppendSnapshot(ctx, store.Snapshot{
		ProductID: p2.ID, CapturedAt: base, LooseCents: &other,
	}); err != nil {
		t.Fatal(err)
	}

	var count int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM price_snapshots WHERE product_id = $1", p1.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("want 3 snapshots for the product, got %d", count)
	}

	var roundTripped store.Snapshot
	err = pool.QueryRow(ctx,
		`SELECT product_id, captured_at, loose_cents, cib_cents, new_cents
		FROM price_snapshots WHERE product_id = $1 ORDER BY captured_at LIMIT 1`, p1.ID).
		Scan(&roundTripped.ProductID, &roundTripped.CapturedAt,
			&roundTripped.LooseCents, &roundTripped.CIBCents, &roundTripped.NewCents)
	if err != nil {
		t.Fatal(err)
	}
	if roundTripped.LooseCents == nil {
		t.Fatal("snapshot fields lost in the round-trip")
	}
}
