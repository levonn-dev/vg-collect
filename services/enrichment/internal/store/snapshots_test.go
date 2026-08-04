package store_test

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/levonn-dev/vgkeep/services/enrichment/internal/store"
)

func TestSnapshots_AppendIntoTimeSeries(t *testing.T) {
	s, mdb := newTestStore(t)
	ctx := context.Background()

	loose := int64(4200)
	base := time.Date(2026, 7, 1, 6, 0, 0, 0, time.UTC)
	for day := range 3 {
		v := loose + int64(day)*10
		snap := store.Snapshot{
			ProductID:  "11111111-1111-1111-1111-111111111111",
			CapturedAt: base.AddDate(0, 0, day),
			LooseCents: &v,
		}
		if err := s.AppendSnapshot(ctx, snap); err != nil {
			t.Fatal(err)
		}
	}
	// One point for a different product, to prove metaField filtering.
	other := int64(100)
	if err := s.AppendSnapshot(ctx, store.Snapshot{
		ProductID: "22222222-2222-2222-2222-222222222222", CapturedAt: base, LooseCents: &other,
	}); err != nil {
		t.Fatal(err)
	}

	count, err := mdb.Collection("price_snapshots").CountDocuments(ctx,
		bson.D{{Key: "product_id", Value: "11111111-1111-1111-1111-111111111111"}})
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("want 3 snapshots for the product, got %d", count)
	}
	var roundTripped store.Snapshot
	err = mdb.Collection("price_snapshots").FindOne(ctx,
		bson.D{{Key: "product_id", Value: "11111111-1111-1111-1111-111111111111"}}).Decode(&roundTripped)
	if err != nil {
		t.Fatal(err)
	}
	if roundTripped.LooseCents == nil {
		t.Fatal("snapshot fields lost in the time-series round-trip")
	}
}
