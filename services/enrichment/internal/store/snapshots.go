package store

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Snapshot is one point in the price_snapshots time-series, keyed by
// our product id (the metaField). Appends are the only write shape:
// the series stays delta-friendly for a future notifications feature.
type Snapshot struct {
	ProductID  string    `bson:"product_id"`
	CapturedAt time.Time `bson:"captured_at"`
	LooseCents *int64    `bson:"loose_cents,omitempty"`
	CIBCents   *int64    `bson:"cib_cents,omitempty"`
	NewCents   *int64    `bson:"new_cents,omitempty"`
}

// AppendSnapshot inserts one time-series point.
func (s *Store) AppendSnapshot(ctx context.Context, snap Snapshot) error {
	snap.CapturedAt = snap.CapturedAt.UTC().Truncate(time.Millisecond)
	if _, err := s.db.Collection(colSnapshots).InsertOne(ctx, snap); err != nil {
		return fmt.Errorf("store: append snapshot: %w", err)
	}
	return nil
}

// SnapshotsSince returns each product's snapshots captured at or after
// since, oldest first, grouped by product id. Ids without in-window
// points are absent from the map.
func (s *Store) SnapshotsSince(ctx context.Context, ids []string, since time.Time) (map[string][]Snapshot, error) {
	out := map[string][]Snapshot{}
	if len(ids) == 0 {
		return out, nil
	}
	cur, err := s.db.Collection(colSnapshots).Find(ctx,
		bson.M{"product_id": bson.M{"$in": ids}, "captured_at": bson.M{"$gte": since.UTC()}},
		options.Find().SetSort(bson.D{{Key: "captured_at", Value: 1}}))
	if err != nil {
		return nil, fmt.Errorf("store: snapshots since: %w", err)
	}
	var snaps []Snapshot
	if err := cur.All(ctx, &snaps); err != nil {
		return nil, fmt.Errorf("store: decode snapshots: %w", err)
	}
	for _, sn := range snaps {
		out[sn.ProductID] = append(out[sn.ProductID], sn)
	}
	return out, nil
}
