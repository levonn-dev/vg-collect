package store

import (
	"context"
	"fmt"
	"time"
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
