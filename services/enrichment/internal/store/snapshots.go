package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Snapshot is one point in the price_snapshots time-series, keyed by
// our product id; appends are the only write shape.
type Snapshot struct {
	ProductID  string
	CapturedAt time.Time
	LooseCents *int64
	CIBCents   *int64
	NewCents   *int64
}

// AppendSnapshot inserts one snapshot row. A duplicate (product_id,
// captured_at) is dropped: identical-instant re-appends carry no new fact.
func (s *Store) AppendSnapshot(ctx context.Context, snap Snapshot) error {
	if _, err := s.pool.Exec(ctx, `INSERT INTO price_snapshots
		(product_id, captured_at, loose_cents, cib_cents, new_cents)
		VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
		snap.ProductID, snap.CapturedAt.UTC().Truncate(time.Millisecond),
		snap.LooseCents, snap.CIBCents, snap.NewCents); err != nil {
		return fmt.Errorf("store: append snapshot: %w", err)
	}
	return nil
}

// SnapshotsSince returns each product's snapshots at or after since,
// oldest first; ids without in-window points are absent from the map.
func (s *Store) SnapshotsSince(ctx context.Context, ids []string, since time.Time) (map[string][]Snapshot, error) {
	out := map[string][]Snapshot{}
	if len(ids) == 0 {
		return out, nil
	}
	snaps, err := queryAll(ctx, s.pool, `SELECT product_id, captured_at, loose_cents, cib_cents, new_cents
		FROM price_snapshots WHERE product_id = ANY($1) AND captured_at >= $2
		ORDER BY captured_at`, []any{ids, since.UTC()},
		func(rows pgx.Rows) (Snapshot, error) {
			var sn Snapshot
			err := rows.Scan(&sn.ProductID, &sn.CapturedAt, &sn.LooseCents, &sn.CIBCents, &sn.NewCents)
			sn.CapturedAt = sn.CapturedAt.UTC()
			return sn, err
		}, "snapshots since")
	if err != nil {
		return nil, err
	}
	for _, sn := range snaps {
		out[sn.ProductID] = append(out[sn.ProductID], sn)
	}
	return out, nil
}
